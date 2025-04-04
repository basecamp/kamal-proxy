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
	name         string
	hosts        []string
	pathPrefixes []string
	options      ServiceOptions

	activePool  *TargetPool
	rolloutPool *TargetPool
	targetLock  sync.RWMutex

	pauseController   *PauseController
	rolloutController *RolloutController
	certManager       CertManager
	middleware        http.Handler
}

func NewService(name string, hosts []string, pathPrefixes []string, options ServiceOptions) (*Service, error) {
	service := &Service{
		name:            name,
		pauseController: NewPauseController(),
		activePool:      NewTargetPool(),
		rolloutPool:     NewTargetPool(),
	}

	hosts = NormalizeHosts(hosts)
	pathPrefixes = NormalizePathPrefixes(pathPrefixes)

	err := service.initialize(hosts, pathPrefixes, options)
	if err != nil {
		return nil, err
	}

	return service, nil
}

func (s *Service) UpdateOptions(hosts []string, pathPrefixes []string, options ServiceOptions) error {
	hosts = NormalizeHosts(hosts)
	pathPrefixes = NormalizePathPrefixes(pathPrefixes)

	return s.initialize(hosts, pathPrefixes, options)
}

func (s *Service) ActiveTargets() []*Target {
	s.targetLock.RLock()
	defer s.targetLock.RUnlock()

	return s.activePool.GetTargets()
}

func (s *Service) RolloutTargets() []*Target {
	s.targetLock.RLock()
	defer s.targetLock.RUnlock()

	return s.rolloutPool.GetTargets()
}

func (s *Service) ClaimTarget(req *http.Request) (*Target, *http.Request, error) {
	s.targetLock.RLock()
	defer s.targetLock.RUnlock()

	var targetPool *TargetPool
	targetPool = s.activePool

	if s.rolloutPool.Count() > 0 && s.rolloutController != nil && s.rolloutController.RequestUsesRolloutGroup(req) {
		slog.Debug("Using rollout target pool for request", "service", s.name, "path", req.URL.Path)
		targetPool = s.rolloutPool
	}

	return targetPool.StartRequest(req)
}

// SetTarget adds a target to the specified slot's pool.
// This method simply adds the target to the appropriate pool.
// Returns nil as we no longer track or return "old" targets.
func (s *Service) SetTarget(slot TargetSlot, target *Target) *Target {
	s.withWriteLock(func() error {
		switch slot {
		case TargetSlotActive:
			s.activePool.AddTarget(target)

		case TargetSlotRollout:
			s.rolloutPool.AddTarget(target)
		}

		return nil
	})

	// We no longer return any target
	// Callers should use ActiveTargets() or RolloutTargets() to access all targets
	return nil
}

func (s *Service) SetRolloutSplit(percentage int, allowlist []string) error {
	s.targetLock.Lock()
	defer s.targetLock.Unlock()

	if s.rolloutPool.Count() == 0 {
		return ErrorRolloutTargetNotSet
	}

	s.rolloutController = NewRolloutController(percentage, allowlist)
	slog.Info("Set rollout split", "service", s.name, "percentage", percentage, "allowlist", allowlist)
	return nil
}

func (s *Service) StopRollout() error {
	s.targetLock.Lock()
	defer s.targetLock.Unlock()

	rolloutTargets := s.rolloutPool.GetTargets()
	for _, target := range rolloutTargets {
		target.StopHealthChecks()
	}

	s.rolloutPool.ReplaceTargets([]*Target{})
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
	ActiveTargets     []string           `json:"active_targets"`
	RolloutTargets    []string           `json:"rollout_targets,omitempty"`
	Options           ServiceOptions     `json:"options"`
	TargetOptions     TargetOptions      `json:"target_options"`
	PauseController   *PauseController   `json:"pause_controller"`
	RolloutController *RolloutController `json:"rollout_controller"`
}

func (s *Service) MarshalJSON() ([]byte, error) {
	var activeTargetURLs []string
	var rolloutTargetURLs []string
	var targetOptions TargetOptions

	activeTargets := s.activePool.GetTargets()
	if len(activeTargets) > 0 {
		targetOptions = activeTargets[0].options

		for _, t := range activeTargets {
			activeTargetURLs = append(activeTargetURLs, t.Target())
		}
	}

	rolloutTargets := s.rolloutPool.GetTargets()
	if len(rolloutTargets) > 0 {
		for _, t := range rolloutTargets {
			rolloutTargetURLs = append(rolloutTargetURLs, t.Target())
		}
	}

	return json.Marshal(marshalledService{
		Name:              s.name,
		Hosts:             s.hosts,
		PathPrefixes:      s.pathPrefixes,
		ActiveTargets:     activeTargetURLs,
		RolloutTargets:    rolloutTargetURLs,
		Options:           s.options,
		TargetOptions:     targetOptions,
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

	s.initialize(ms.Hosts, ms.PathPrefixes, ms.Options)

	if s.activePool == nil {
		s.activePool = NewTargetPool()
	}

	if s.rolloutPool == nil {
		s.rolloutPool = NewTargetPool()
	}

	for _, targetURL := range ms.ActiveTargets {
		s.restoreSavedTarget(TargetSlotActive, targetURL, ms.TargetOptions)
	}

	for _, targetURL := range ms.RolloutTargets {
		s.restoreSavedTarget(TargetSlotRollout, targetURL, ms.TargetOptions)
	}

	return nil
}

func (s *Service) Stop(drainTimeout time.Duration, message string) error {
	err := s.pauseController.Stop(message)
	if err != nil {
		return err
	}

	slog.Info("Service stopped", "service", s.name)

	for _, target := range s.ActiveTargets() {
		target.Drain(drainTimeout)
	}
	slog.Info("Service drained", "service", s.name)
	return nil
}

func (s *Service) Pause(drainTimeout time.Duration, pauseTimeout time.Duration) error {
	err := s.pauseController.Pause(pauseTimeout)
	if err != nil {
		return err
	}

	slog.Info("Service paused", "service", s.name)

	for _, target := range s.ActiveTargets() {
		target.Drain(drainTimeout)
	}
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

func (s *Service) initialize(hosts []string, pathPrefixes []string, options ServiceOptions) error {
	certManager, err := s.createCertManager(hosts, options)
	if err != nil {
		return err
	}

	middleware, err := s.createMiddleware(options, certManager)
	if err != nil {
		return err
	}

	s.hosts = hosts
	s.pathPrefixes = pathPrefixes
	s.options = options
	s.certManager = certManager
	s.middleware = middleware

	return nil
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

	target, req, err := s.ClaimTarget(r)
	if err != nil {
		SetErrorResponse(w, req, http.StatusServiceUnavailable, nil)
		return
	}

	target.SendRequest(w, req)
}

func (s *Service) shouldRedirectToHTTPS(r *http.Request) bool {
	return s.options.TLSEnabled && s.options.TLSRedirect && r.TLS == nil
}

func (s *Service) handlePausedAndStoppedRequests(w http.ResponseWriter, r *http.Request) bool {
	targets := s.activePool.GetTargets()

	if s.pauseController.GetState() != PauseStateRunning && len(targets) > 0 && targets[0].IsHealthCheckRequest(r) {
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

func (s *Service) restoreSavedTarget(slot TargetSlot, savedTarget string, options TargetOptions) error {
	if savedTarget == "" {
		return nil // Nothing to restore
	}

	target, err := NewTarget(savedTarget, options)
	if err != nil {
		return err
	}

	// Restored targets are always considered healthy, because they would have
	// been that way when they were saved.
	target.state = TargetStateHealthy

	switch slot {
	case TargetSlotActive:
		s.activePool.ReplaceTargets([]*Target{target})

	case TargetSlotRollout:
		s.rolloutPool.ReplaceTargets([]*Target{target})
	}

	return nil
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

func (s *Service) withWriteLock(fn func() error) error {
	s.targetLock.Lock()
	defer s.targetLock.Unlock()

	return fn()
}
