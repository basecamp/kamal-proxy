package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

const (
	B  int64 = 1
	KB       = B << 10
	MB       = KB << 10
	GB       = MB << 10
)

const (
	DefaultDeployTimeout = time.Second * 30
	DefaultDrainTimeout  = time.Second * 30
	DefaultPauseTimeout  = time.Second * 30

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckInterval = time.Second
	DefaultHealthCheckTimeout  = time.Second * 5

	MaxIdleConnsPerHost = 100
	ProxyBufferSize     = 32 * KB

	DefaultTargetTimeout       = time.Second * 30
	DefaultMaxMemoryBufferSize = 1 * MB
	DefaultMaxRequestBodySize  = 0
	DefaultMaxResponseBodySize = 0

	DefaultStopMessage = ""
)

var (
	ErrorRolloutTargetNotSet                 = errors.New("rollout target not set")
	ErrorUnableToLoadErrorPages              = errors.New("unable to load error pages")
	ErrorAutomaticTLSDoesNotSupportWildcards = errors.New("automatic TLS does not support wildcards")
)

type TargetSlot int

const (
	TargetSlotActive TargetSlot = iota
	TargetSlotRollout
)

type HealthCheckConfig struct {
	Path     string        `json:"path"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
}

type ServiceOptions struct {
	TLSEnabled         bool   `json:"tls_enabled"`
	TLSCertificatePath string `json:"tls_certificate_path"`
	TLSPrivateKeyPath  string `json:"tls_private_key_path"`
	TLSRedirect        bool   `json:"tls_redirect"`
	ACMEDirectory      string `json:"acme_directory"`
	ACMECachePath      string `json:"acme_cache_path"`
	ErrorPagePath      string `json:"error_page_path"`
	StripPrefix        bool   `json:"strip_prefix"`
}

func (so ServiceOptions) ScopedCachePath() string {
	// We need to scope our certificate cache according to whatever ACME settings
	// we want to use, such as the directory.  This is so we can reuse
	// certificates between deployments when the settings are the same, but
	// provision new certificates when they change.

	hasher := sha256.New()
	hasher.Write([]byte(so.ACMEDirectory))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return path.Join(so.ACMECachePath, hash)
}

type Service struct {
	name          string
	hosts         []string
	pathPrefixes  []string
	options       ServiceOptions
	targetOptions TargetOptions

	active  *LoadBalancer
	rollout *LoadBalancer

	pauseController   *PauseController
	rolloutController *RolloutController
	serviceLock       sync.Mutex

	certManager CertManager
	middleware  http.Handler
}

func NewService(name string, hosts []string, pathPrefixes []string, options ServiceOptions, targetOptions TargetOptions) (*Service, error) {
	hosts = NormalizeHosts(hosts)
	pathPrefixes = NormalizePathPrefixes(pathPrefixes)

	service := &Service{
		name:            name,
		hosts:           hosts,
		pathPrefixes:    pathPrefixes,
		options:         options,
		targetOptions:   targetOptions,
		pauseController: NewPauseController(),
	}

	return service, service.initialize()
}

func (s *Service) CopyWithOptions(hosts []string, pathPrefixes []string, options ServiceOptions, targetOptions TargetOptions) (*Service, error) {
	service, err := NewService(s.name, hosts, pathPrefixes, options, targetOptions)
	if err != nil {
		return nil, err
	}

	service.active = s.active
	service.rollout = s.rollout
	service.pauseController = s.pauseController
	service.rolloutController = s.rolloutController

	return service, service.initialize()
}

func (s *Service) Dispose() {
	s.active.Dispose()
	if s.rollout != nil {
		s.rollout.Dispose()
	}
}

func (s *Service) UpdateLoadBalancer(lb *LoadBalancer, slot TargetSlot) *LoadBalancer {
	s.serviceLock.Lock()
	defer s.serviceLock.Unlock()

	var replaced *LoadBalancer

	if slot == TargetSlotRollout {
		replaced = s.rollout
		s.rollout = lb
	} else {
		replaced = s.active
		s.active = lb
	}

	return replaced
}

func (s *Service) SetRolloutSplit(percentage int, allowlist []string) error {
	s.serviceLock.Lock()
	defer s.serviceLock.Unlock()

	if s.rollout == nil {
		return ErrorRolloutTargetNotSet
	}

	s.rolloutController = NewRolloutController(percentage, allowlist)
	slog.Info("Set rollout split", "service", s.name, "percentage", percentage, "allowlist", allowlist)
	return nil
}

func (s *Service) StopRollout() error {
	s.serviceLock.Lock()
	defer s.serviceLock.Unlock()

	s.rolloutController = nil
	slog.Info("Stopped rollout", "service", s.name)
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.middleware.ServeHTTP(w, r)
}

type marshalledService struct {
	Name              string             `json:"name"`
	Hosts             []string           `json:"hosts"`
	PathPrefixes      []string           `json:"path_prefixes"`
	Options           ServiceOptions     `json:"options"`
	TargetOptions     TargetOptions      `json:"target_options"`
	ActiveTargets     []string           `json:"active_targets"`
	RolloutTargets    []string           `json:"rollout_targets"`
	PauseController   *PauseController   `json:"pause_controller"`
	RolloutController *RolloutController `json:"rollout_controller"`

	LegacyActiveTarget  string `json:"active_target,omitempty"`
	LegacyRolloutTarget string `json:"rollout_target,omitempty"`
}

func (s *Service) MarshalJSON() ([]byte, error) {
	var rolloutTargets []string
	if s.rollout != nil {
		rolloutTargets = s.rollout.Targets().Names()
	}

	return json.Marshal(marshalledService{
		Name:              s.name,
		Hosts:             s.hosts,
		PathPrefixes:      s.pathPrefixes,
		ActiveTargets:     s.active.Targets().Names(),
		RolloutTargets:    rolloutTargets,
		Options:           s.options,
		TargetOptions:     s.targetOptions,
		PauseController:   s.pauseController,
		RolloutController: s.rolloutController,
	})
}

func (s *Service) UnmarshalJSON(data []byte) error {
	var ms marshalledService
	err := json.Unmarshal(data, &ms)
	if err != nil {
		return err
	}

	s.name = ms.Name
	s.pauseController = ms.PauseController
	s.rolloutController = ms.RolloutController
	s.hosts = ms.Hosts
	s.pathPrefixes = ms.PathPrefixes
	s.options = ms.Options
	s.targetOptions = ms.TargetOptions

	if len(ms.ActiveTargets) == 0 && ms.LegacyActiveTarget != "" {
		ms.ActiveTargets = []string{ms.LegacyActiveTarget}
	}
	if len(ms.RolloutTargets) == 0 && ms.LegacyRolloutTarget != "" {
		ms.RolloutTargets = []string{ms.LegacyRolloutTarget}
	}

	activeTargets, err := NewTargetList(ms.ActiveTargets, ms.TargetOptions)
	if err != nil {
		return err
	}
	s.active = NewLoadBalancer(activeTargets)
	s.active.MarkAllHealthy()

	rolloutTargets, err := NewTargetList(ms.RolloutTargets, ms.TargetOptions)
	if err != nil {
		return err
	}
	s.rollout = NewLoadBalancer(rolloutTargets)
	s.rollout.MarkAllHealthy()

	return s.initialize()
}

func (s *Service) Stop(drainTimeout time.Duration, message string) error {
	err := s.pauseController.Stop(message)
	if err != nil {
		return err
	}

	slog.Info("Service stopped", "service", s.name)

	s.Drain(drainTimeout)
	slog.Info("Service drained", "service", s.name)
	return nil
}

func (s *Service) Pause(drainTimeout time.Duration, pauseTimeout time.Duration) error {
	err := s.pauseController.Pause(pauseTimeout)
	if err != nil {
		return err
	}

	slog.Info("Service paused", "service", s.name)

	s.Drain(drainTimeout)
	slog.Info("Service drained", "service", s.name)
	return nil
}

func (s *Service) Resume() error {
	err := s.pauseController.Resume()
	if err != nil {
		return err
	}

	slog.Info("Service resumed", "service", s.name)
	return nil
}

// Private

func (s *Service) initialize() error {
	certManager, err := s.createCertManager(s.hosts, s.options)
	if err != nil {
		return err
	}

	middleware, err := s.createMiddleware(s.options, certManager)
	if err != nil {
		return err
	}

	s.certManager = certManager
	s.middleware = middleware

	return nil
}

func (s *Service) Drain(timeout time.Duration) {
	PerformConcurrently(
		func() {
			s.active.DrainAll(timeout)
		},
		func() {
			if s.rollout != nil {
				s.rollout.DrainAll(timeout)
			}
		},
	)
}

func (s *Service) loadBalancerForRequest(req *http.Request) *LoadBalancer {
	lb := s.active
	if s.rollout != nil && s.rolloutController != nil && s.rolloutController.RequestUsesRolloutGroup(req) {
		slog.Debug("Using rollout for request", "service", s.name, "path", req.URL.Path)
		lb = s.rollout
	}

	return lb
}

func (s *Service) servesRootPath() bool {
	return slices.Contains(s.pathPrefixes, rootPath)
}

func (s *Service) createCertManager(hosts []string, options ServiceOptions) (CertManager, error) {
	if !options.TLSEnabled {
		return nil, nil
	}

	if options.TLSCertificatePath != "" && options.TLSPrivateKeyPath != "" {
		return NewStaticCertManager(options.TLSCertificatePath, options.TLSPrivateKeyPath)
	}

	// Ensure we're not trying to use Let's Encrypt to fetch a wildcard domain,
	// as that is not supported with the challenge types that we use.
	for _, host := range hosts {
		if strings.Contains(host, "*") {
			return nil, ErrorAutomaticTLSDoesNotSupportWildcards
		}
	}

	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(options.ScopedCachePath()),
		HostPolicy: autocert.HostWhitelist(hosts...),
		Client:     &acme.Client{DirectoryURL: options.ACMEDirectory},
	}, nil
}

func (s *Service) createMiddleware(options ServiceOptions, certManager CertManager) (http.Handler, error) {
	var err error
	var handler http.Handler = http.HandlerFunc(s.serviceRequestWithTarget)

	if options.ErrorPagePath != "" {
		slog.Debug("Using custom error pages", "service", s.name, "path", options.ErrorPagePath)
		errorPageFS := os.DirFS(options.ErrorPagePath)
		handler, err = WithErrorPageMiddleware(errorPageFS, false, handler)
		if err != nil {
			slog.Error("Unable to parse custom error pages", "service", s.name, "path", options.ErrorPagePath, "error", err)
			return nil, ErrorUnableToLoadErrorPages
		}
	}

	if certManager != nil {
		slog.Debug("Using ACME handler", "service", s.name)
		handler = certManager.HTTPHandler(handler)
	}

	return handler, nil
}

func (s *Service) serviceRequestWithTarget(w http.ResponseWriter, r *http.Request) {
	LoggingRequestContext(r).Service = s.name

	if s.shouldRedirectToHTTPS(r) {
		s.redirectToHTTPS(w, r)
		return
	}

	if !s.options.TLSEnabled && r.TLS != nil {
		SetErrorResponse(w, r, http.StatusServiceUnavailable, nil)
		return
	}

	if s.handlePausedAndStoppedRequests(w, r) {
		return
	}

	lb := s.loadBalancerForRequest(r)
	lb.ServeHTTP(w, r)
}

func (s *Service) shouldRedirectToHTTPS(r *http.Request) bool {
	return s.options.TLSEnabled && s.options.TLSRedirect && r.TLS == nil
}

func (s *Service) handlePausedAndStoppedRequests(w http.ResponseWriter, r *http.Request) bool {
	if s.pauseController.GetState() != PauseStateRunning && s.targetOptions.IsHealthCheckRequest(r) {
		// When paused or stopped, return success for any health check
		// requests from downstream services. Otherwise, they might consider
		// us as unhealthy while in that state, and remove us from their
		// pool.
		w.WriteHeader(http.StatusOK)
		return true
	}

	action, message := s.pauseController.Wait()
	switch action {
	case PauseWaitActionStopped:
		templateArguments := struct{ Message string }{message}
		SetErrorResponse(w, r, http.StatusServiceUnavailable, templateArguments)
		return true

	case PauseWaitActionTimedOut:
		slog.Warn("Rejecting request due to expired pause", "service", s.name, "path", r.URL.Path)
		SetErrorResponse(w, r, http.StatusGatewayTimeout, nil)
		return true
	}

	return false
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
