package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"path"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const (
	DefaultDeployTimeout = time.Second * 30
	DefaultDrainTimeout  = time.Second * 10
	DefaultPauseTimeout  = time.Second * 30

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckInterval = time.Second
	DefaultHealthCheckTimeout  = time.Second * 5

	MaxIdleConnsPerHost = 100

	DefaultTargetTimeout = time.Second * 10
)

type HealthCheckConfig struct {
	Path     string        `json:"path"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
}

type ServiceOptions struct {
	HealthCheckConfig  HealthCheckConfig `json:"health_check"`
	MaxRequestBodySize int64             `json:"max_request_body_size"`
	TargetTimeout      time.Duration     `json:"target_timeout"`
	TLSHostname        string            `json:"tls_hostname"`
	ACMEDirectory      string            `json:"acme_directory"`
	ACMECachePath      string            `json:"acme_cache_path"`
}

func (to ServiceOptions) RequireTLS() bool {
	return to.TLSHostname != ""
}

func (to ServiceOptions) ScopedCachePath() string {
	// We need to scope our certificate cache according to whatever ACME settings
	// we want to use, such as the directory.  This is so we can reuse
	// certificates between deployments when the settings are the same, but
	// provision new certificates when they change.

	hasher := sha256.New()
	hasher.Write([]byte(to.ACMEDirectory))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return path.Join(to.ACMECachePath, hash)
}

type Service struct {
	name    string
	host    string
	options ServiceOptions

	active     *Target
	targetLock sync.RWMutex

	pauseControl *PauseControl
	certManager  *autocert.Manager
}

func NewService(name, host string, options ServiceOptions) *Service {
	service := &Service{
		name:    name,
		host:    host,
		options: options,
	}

	service.initialize()

	return service
}

func (s *Service) UpdateOptions(options ServiceOptions) {
	s.options = options
	s.certManager = s.createCertManager()
}

func (s *Service) ActiveTarget() *Target {
	s.targetLock.RLock()
	defer s.targetLock.RUnlock()

	return s.active
}

func (s *Service) ClaimTarget(req *http.Request) (*Target, *http.Request, error) {
	s.targetLock.RLock()
	defer s.targetLock.RUnlock()

	req, err := s.active.StartRequest(req)
	return s.active, req, err
}

func (s *Service) SetActiveTarget(target *Target, drainTimeout time.Duration) {
	s.targetLock.Lock()
	defer s.targetLock.Unlock()

	if s.active != nil {
		s.active.StopHealthChecks()
		go s.active.Drain(drainTimeout)
	}

	s.active = target
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.recordServiceNameForRequest(r)

	if s.options.RequireTLS() && r.TLS == nil {
		s.redirectToHTTPS(w, r)
		return
	}

	proceed := s.pauseControl.Wait()
	if !proceed {
		slog.Warn("Rejecting request due to expired pause", "service", s.name, "path", r.URL.Path)
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	r = s.limitRequestBody(w, r)

	target, req, err := s.ClaimTarget(r)
	if err != nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	target.SendRequest(w, req)
}

type marshalledService struct {
	Name         string         `json:"name"`
	Host         string         `json:"host"`
	Options      ServiceOptions `json:"options"`
	ActiveTarget string         `json:"active_target"`
}

func (s *Service) MarshalJSON() ([]byte, error) {
	return json.Marshal(marshalledService{
		Name:         s.name,
		Host:         s.host,
		Options:      s.options,
		ActiveTarget: s.ActiveTarget().Target(),
	})
}

func (s *Service) UnmarshalJSON(data []byte) error {
	var ms marshalledService
	err := json.Unmarshal(data, &ms)
	if err != nil {
		return err
	}

	targetOptions := TargetOptions{
		HealthCheckConfig: s.options.HealthCheckConfig,
		ResponseTimeout:   s.options.TargetTimeout,
	}

	active, err := NewTarget(ms.ActiveTarget, targetOptions)
	if err != nil {
		return err
	}

	// Restored targets are always considered healthy, because they would have
	// been that way when they were saved.
	active.state = TargetStateHealthy

	s.name = ms.Name
	s.host = ms.Host
	s.options = ms.Options
	s.active = active

	s.initialize()

	return nil
}

func (s *Service) Pause(drainTimeout time.Duration, pauseTimeout time.Duration) error {
	err := s.pauseControl.Pause(pauseTimeout)
	if err != nil {
		return err
	}

	slog.Info("Service paused", "service", s.name)

	s.ActiveTarget().Drain(drainTimeout)
	slog.Info("Service drained", "service", s.name)
	return nil
}

func (s *Service) Resume() error {
	err := s.pauseControl.Resume()
	if err != nil {
		return err
	}

	slog.Info("Service resumed", "service", s.name)
	return nil
}

// Private

func (s *Service) initialize() {
	s.pauseControl = NewPauseControl()
	s.certManager = s.createCertManager()
}

func (s *Service) recordServiceNameForRequest(req *http.Request) {
	serviceIdentifer, ok := req.Context().Value(contextKeyService).(*string)
	if ok {
		*serviceIdentifer = s.name
	}
}

func (s *Service) createCertManager() *autocert.Manager {
	if s.options.TLSHostname == "" {
		return nil
	}

	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(s.options.ScopedCachePath()),
		HostPolicy: autocert.HostWhitelist(s.options.TLSHostname),
		Client:     &acme.Client{DirectoryURL: s.options.ACMEDirectory},
	}
}

func (s *Service) redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "close")

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	url := "https://" + host + r.URL.RequestURI()
	http.Redirect(w, r, url, http.StatusMovedPermanently)
}

func (s *Service) limitRequestBody(w http.ResponseWriter, r *http.Request) *http.Request {
	if s.options.MaxRequestBodySize > 0 {
		r2 := *r // Copy so we aren't modifying the original request
		r2.Body = http.MaxBytesReader(w, r.Body, s.options.MaxRequestBodySize)
		r = &r2
	}

	return r
}
