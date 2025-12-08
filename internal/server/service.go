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

	"github.com/basecamp/kamal-proxy/internal/metrics"
)

const (
	B  int64 = 1
	KB       = B << 10
	MB       = KB << 10
	GB       = MB << 10
)

const (
	DefaultDeployTimeout         = time.Second * 30
	DefaultDrainTimeout          = time.Second * 30
	DefaultPauseTimeout          = time.Second * 30
	DefaultWriterAffinityTimeout = time.Second * 3

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckPort     = 0
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
	Port     int           `json:"port"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
	Host     string        `json:"host"`
}

type DeploymentOptions struct {
	DeployTimeout time.Duration
	DrainTimeout  time.Duration
	Force         bool
}

type ServiceOptions struct {
	Hosts                       []string      `json:"hosts"`
	PathPrefixes                []string      `json:"path_prefixes"`
	TLSEnabled                  bool          `json:"tls_enabled"`
	TLSCertificatePath          string        `json:"tls_certificate_path"`
	TLSPrivateKeyPath           string        `json:"tls_private_key_path"`
	TLSRedirect                 bool          `json:"tls_redirect"`
	CanonicalHost               string        `json:"canonical_host"`
	ACMEDirectory               string        `json:"acme_directory"`
	ACMECachePath               string        `json:"acme_cache_path"`
	ErrorPagePath               string        `json:"error_page_path"`
	StripPrefix                 bool          `json:"strip_prefix"`
	WriterAffinityTimeout       time.Duration `json:"writer_affinity_timeout"`
	ReadTargetsAcceptWebsockets bool          `json:"read_targets_accept_websockets"`
}

func (so *ServiceOptions) Normalize() {
	so.Hosts = NormalizeHosts(so.Hosts)
	so.PathPrefixes = NormalizePathPrefixes(so.PathPrefixes)
}

func (so *ServiceOptions) WithHosts(hosts []string) ServiceOptions {
	options := *so
	options.Hosts = hosts
	return options
}

func (so *ServiceOptions) WithPathPrefixes(pathPrefixes []string) ServiceOptions {
	options := *so
	options.PathPrefixes = pathPrefixes
	return options
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
	options       ServiceOptions
	targetOptions TargetOptions

	active      *LoadBalancer
	rollout     *LoadBalancer
	serviceLock sync.RWMutex

	pauseController   *PauseController
	rolloutController *RolloutController

	certManager CertManager
	middleware  http.Handler
}

func NewService(name string, options ServiceOptions, targetOptions TargetOptions) (*Service, error) {
	service := &Service{
		name:            name,
		pauseController: NewPauseController(),
	}

	if err := service.initialize(options, targetOptions); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) UpdateOptions(options ServiceOptions, targetOptions TargetOptions) error {
	return s.initialize(options, targetOptions)
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
	metrics.Tracker.AddInflightRequest(s.name)
	defer metrics.Tracker.SubtractInflightRequest(s.name)

	s.middleware.ServeHTTP(w, r)
}

type marshalledService struct {
	Name              string             `json:"name"`
	Options           ServiceOptions     `json:"options"`
	TargetOptions     TargetOptions      `json:"target_options"`
	ActiveTargets     []string           `json:"active_targets"`
	ActiveReaders     []string           `json:"active_readers"`
	RolloutTargets    []string           `json:"rollout_targets"`
	RolloutReaders    []string           `json:"rollout_readers"`
	PauseController   *PauseController   `json:"pause_controller"`
	RolloutController *RolloutController `json:"rollout_controller"`

	LegacyActiveTarget  string   `json:"active_target,omitempty"`
	LegacyRolloutTarget string   `json:"rollout_target,omitempty"`
	LegacyHosts         []string `json:"hosts,omitempty"`
	LegacyPathPrefixes  []string `json:"path_prefixes,omitempty"`
}

func (s *Service) MarshalJSON() ([]byte, error) {
	var rolloutTargets []string
	var rolloutReaders []string
	if s.rollout != nil {
		rolloutTargets = s.rollout.WriteTargets().Names()
		rolloutReaders = s.rollout.ReadTargets().Names()
	}

	return json.Marshal(marshalledService{
		Name:              s.name,
		ActiveTargets:     s.active.WriteTargets().Names(),
		ActiveReaders:     s.active.ReadTargets().Names(),
		RolloutTargets:    rolloutTargets,
		RolloutReaders:    rolloutReaders,
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

	// Support previous version of service state
	if len(ms.ActiveTargets) == 0 && ms.LegacyActiveTarget != "" {
		ms.ActiveTargets = []string{ms.LegacyActiveTarget}
	}
	if len(ms.RolloutTargets) == 0 && ms.LegacyRolloutTarget != "" {
		ms.RolloutTargets = []string{ms.LegacyRolloutTarget}
	}
	if len(ms.Options.Hosts) == 0 && len(ms.LegacyHosts) > 0 {
		ms.Options.Hosts = ms.LegacyHosts
	}
	if len(ms.Options.PathPrefixes) == 0 && len(ms.LegacyPathPrefixes) > 0 {
		ms.Options.PathPrefixes = ms.LegacyPathPrefixes
	}
	ms.Options.Normalize()

	s.name = ms.Name
	s.pauseController = ms.PauseController
	s.rolloutController = ms.RolloutController

	activeTargets, err := NewTargetList(ms.ActiveTargets, ms.ActiveReaders, ms.TargetOptions)
	if err != nil {
		return err
	}
	s.active = NewLoadBalancer(activeTargets, ms.Options.WriterAffinityTimeout, ms.Options.ReadTargetsAcceptWebsockets)
	s.active.MarkAllHealthy()

	rolloutTargets, err := NewTargetList(ms.RolloutTargets, ms.RolloutReaders, ms.TargetOptions)
	if err != nil {
		return err
	}
	if len(rolloutTargets) > 0 {
		s.rollout = NewLoadBalancer(rolloutTargets, ms.Options.WriterAffinityTimeout, ms.Options.ReadTargetsAcceptWebsockets)
		s.rollout.MarkAllHealthy()
	}

	return s.initialize(ms.Options, ms.TargetOptions)
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

func (s *Service) initialize(options ServiceOptions, targetOptions TargetOptions) error {
	certManager, err := s.createCertManager(options)
	if err != nil {
		return err
	}

	middleware, err := s.createMiddleware(options, certManager)
	if err != nil {
		return err
	}

	s.options = options
	s.targetOptions = targetOptions
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
	return slices.Contains(s.options.PathPrefixes, rootPath)
}

func (s *Service) createCertManager(options ServiceOptions) (CertManager, error) {
	if !options.TLSEnabled {
		return nil, nil
	}

	if options.TLSCertificatePath != "" && options.TLSPrivateKeyPath != "" {
		return NewStaticCertManager(options.TLSCertificatePath, options.TLSPrivateKeyPath)
	}

	// Ensure we're not trying to use Let's Encrypt to fetch a wildcard domain,
	// as that is not supported with the challenge types that we use.
	for _, host := range options.Hosts {
		if strings.Contains(host, "*") {
			return nil, ErrorAutomaticTLSDoesNotSupportWildcards
		}
	}

	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(options.ScopedCachePath()),
		HostPolicy: autocert.HostWhitelist(options.Hosts...),
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

	if !s.options.TLSEnabled && r.TLS != nil {
		SetErrorResponse(w, r, http.StatusServiceUnavailable, nil)
		return
	}

	if s.handleRedirectsIfNeeded(w, r) {
		return
	}

	if s.handlePausedAndStoppedRequests(w, r) {
		return
	}

	sendRequest := s.startLoadBalancerRequest(w, r)
	if sendRequest != nil {
		sendRequest()
	}
}

func (s *Service) startLoadBalancerRequest(w http.ResponseWriter, r *http.Request) func() {
	s.serviceLock.RLock()
	defer s.serviceLock.RUnlock()

	lb := s.loadBalancerForRequest(r)
	return lb.StartRequest(w, r)
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

func (s *Service) handleRedirectsIfNeeded(w http.ResponseWriter, r *http.Request) bool {
	if url := s.redirectURLIfNeeded(r); url != "" {
		w.Header().Set("Connection", "close")
		http.Redirect(w, r, url, http.StatusMovedPermanently)
		return true
	}
	return false
}

// redirectURLIfNeeded returns a full absolute URL to redirect to when either
// TLS redirection or canonical host redirection should occur. If no redirect is
// needed, it returns an empty string.
func (s *Service) redirectURLIfNeeded(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	currentScheme := "http"
	if r.TLS != nil {
		currentScheme = "https"
	}

	desiredScheme := currentScheme
	if s.options.TLSEnabled && s.options.TLSRedirect && currentScheme == "http" {
		desiredScheme = "https"
	}

	desiredHost := host
	if s.options.CanonicalHost != "" && host != s.options.CanonicalHost {
		desiredHost = s.options.CanonicalHost
	}

	if desiredScheme != currentScheme || desiredHost != host {
		return desiredScheme + "://" + desiredHost + r.URL.RequestURI()
	}

	return ""
}
