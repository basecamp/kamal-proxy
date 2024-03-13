package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sync"
	"time"
)

const (
	DefaultAddTimeout   = time.Second * 5
	DefaultDrainTimeout = time.Second * 5

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckInterval = time.Second
	DefaultHealthCheckTimeout  = time.Second * 5
)

var (
	ErrorInvalidHostPattern = errors.New("invalid host pattern")

	hostRegex = regexp.MustCompile(`^(\w[-_.\w+]+)(:\d+)?$`)
)

type HealthCheckConfig struct {
	Path     string
	Interval time.Duration
	Timeout  time.Duration
}

type TargetState int

const (
	TargetStateAdding TargetState = iota
	TargetStateDraining
	TargetStateHealthy
)

func (s TargetState) String() string {
	switch s {
	case TargetStateAdding:
		return "adding"
	case TargetStateDraining:
		return "draining"
	case TargetStateHealthy:
		return "healthy"
	}
	return ""
}

type inflightMap map[*http.Request]context.CancelFunc

type Target struct {
	targetURL         *url.URL
	healthCheckConfig HealthCheckConfig
	proxy             *httputil.ReverseProxy

	state        TargetState
	inflight     inflightMap
	inflightLock sync.Mutex

	healthcheck   *HealthCheck
	becameHealthy chan (bool)
}

func NewTarget(targetURL string, healthCheckConfig HealthCheckConfig) (*Target, error) {
	uri, err := parseTargetURL(targetURL)
	if err != nil {
		return nil, err
	}

	service := &Target{
		targetURL:         uri,
		healthCheckConfig: healthCheckConfig,

		state:    TargetStateAdding,
		inflight: inflightMap{},
	}

	service.proxy = &httputil.ReverseProxy{
		Rewrite:      service.Rewrite,
		ErrorHandler: service.handleProxyError,
	}

	return service, nil
}

func (s *Target) Target() string {
	return s.targetURL.Host
}

func (s *Target) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req = s.beginInflightRequest(req)
	if req == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer s.endInflightRequest(req)

	s.proxy.ServeHTTP(w, req)
}

func (s *Target) Rewrite(req *httputil.ProxyRequest) {
	// Preserve & append X-Forwarded-For
	req.Out.Header["X-Forwarded-For"] = req.In.Header["X-Forwarded-For"]
	req.SetXForwarded()

	req.SetURL(s.targetURL)
	req.Out.Host = req.In.Host
}

func (s *Target) Drain(timeout time.Duration) {
	s.updateState(TargetStateDraining)

	deadline := time.After(timeout)
	toCancel := s.pendingRequestsToCancel()

WAIT_FOR_REQUESTS_TO_COMPLETE:
	for req := range toCancel {
		select {
		case <-req.Context().Done():
		case <-deadline:
			break WAIT_FOR_REQUESTS_TO_COMPLETE
		}
	}

	for _, cancel := range toCancel {
		cancel()
	}
}

func (s *Target) BeginHealthChecks() {
	s.becameHealthy = make(chan bool)
	s.healthcheck = NewHealthCheck(s,
		s.targetURL.JoinPath(s.healthCheckConfig.Path),
		s.healthCheckConfig.Interval,
		s.healthCheckConfig.Timeout,
	)
}

func (s *Target) StopHealthChecks() {
	if s.healthcheck != nil {
		s.healthcheck.Close()
		s.healthcheck = nil
	}
}

func (s *Target) WaitUntilHealthy(timeout time.Duration) bool {
	select {
	case <-time.After(timeout):
		return false
	case <-s.becameHealthy:
		return true
	}
}

// HealthCheckConsumer

func (s *Target) HealthCheckCompleted(success bool) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	if success && s.state == TargetStateAdding {
		s.state = TargetStateHealthy
		close(s.becameHealthy)
	}

	slog.Info("Target health updated", "target", s.targetURL.Host, "success", success, "state", s.state.String())
}

// Private

func (s *Target) handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("Error while proxying", "target", s.Target(), "path", r.URL.Path, "error", err)

	if s.isRequestEntityTooLarge(err) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	} else {
		w.WriteHeader(http.StatusBadGateway)
	}
}

func (s *Target) isRequestEntityTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func (s *Target) updateState(state TargetState) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	s.state = state
}

func (s *Target) beginInflightRequest(req *http.Request) *http.Request {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	if s.state == TargetStateDraining {
		return nil
	}

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	s.inflight[req] = cancel
	return req
}

func (s *Target) endInflightRequest(req *http.Request) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	cancel := s.inflight[req]
	cancel() // If Drain is waiting on us, let it know we're done

	delete(s.inflight, req)
}

func (s *Target) pendingRequestsToCancel() inflightMap {
	// We use a copy of the inflight map to iterate over while draining, so that
	// we don't need to lock it the whole time, which could interfere with the
	// locking that happens when requests end.
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	result := inflightMap{}
	for k, v := range s.inflight {
		result[k] = v
	}
	return result
}

func parseTargetURL(targetURL string) (*url.URL, error) {
	if !hostRegex.MatchString(targetURL) {
		return nil, fmt.Errorf("%s :%w", targetURL, ErrorInvalidHostPattern)
	}

	uri, _ := url.Parse("http://" + targetURL)
	return uri, nil
}
