package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

var (
	ErrorInvalidHostPattern = errors.New("invalid host pattern")

	hostRegex = regexp.MustCompile(`^(\w[-_.\w+]+)(:\d+)?$`)
)

type HealthCheckConfig struct {
	Path     string        `json:"path"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
}

type TargetOptions struct {
	MaxRequestBodySize int64         `json:"max_request_body_size"`
	TargetTimeout      time.Duration `json:"target_timeout"`
	TLSHostname        string        `json:"tls_hostname"`
	ACMEDirectory      string        `json:"acme_directory"`
	ACMECachePath      string        `json:"acme_cache_path"`
}

func (to TargetOptions) RequireTLS() bool {
	return to.TLSHostname != ""
}

func (to TargetOptions) ScopedCachePath() string {
	// We need to scope our certificate cache according to whatever ACME settings
	// we want to use, such as the directory.  This is so we can reuse
	// certificates between deployments when the settings are the same, but
	// provision new certificates when they change.

	hasher := sha256.New()
	hasher.Write([]byte(to.ACMEDirectory))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return path.Join(to.ACMECachePath, hash)
}

type TargetState int

const (
	TargetStateAdding TargetState = iota
	TargetStateDraining
	TargetStateHealthy
)

func (ts TargetState) String() string {
	switch ts {
	case TargetStateAdding:
		return "adding"
	case TargetStateDraining:
		return "draining"
	case TargetStateHealthy:
		return "healthy"
	}
	return ""
}

type inflightRequest struct {
	cancel   context.CancelFunc
	hijacked bool
}

type inflightMap map[*http.Request]*inflightRequest

type Target struct {
	targetURL         *url.URL
	healthCheckConfig HealthCheckConfig
	options           TargetOptions
	proxyHandler      http.Handler
	certManager       *autocert.Manager

	state        TargetState
	inflight     inflightMap
	inflightLock sync.Mutex

	healthcheck   *HealthCheck
	becameHealthy chan (bool)
}

func NewTarget(targetURL string, healthCheckConfig HealthCheckConfig, options TargetOptions) (*Target, error) {
	uri, err := parseTargetURL(targetURL)
	if err != nil {
		return nil, err
	}

	target := &Target{
		targetURL:         uri,
		healthCheckConfig: healthCheckConfig,
		options:           options,

		state:    TargetStateAdding,
		inflight: inflightMap{},
	}

	target.proxyHandler = target.createProxyHandler()
	target.certManager = target.createCertManager()

	return target, nil
}

func (t *Target) Target() string {
	return t.targetURL.Host
}

func (t *Target) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if t.options.RequireTLS() && req.TLS == nil {
		t.redirectToHTTPS(w, req)
		return
	}

	req, inflightRequest := t.beginInflightRequest(req)

	if req == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer t.endInflightRequest(req)

	targetIdentifer, ok := req.Context().Value(contextKeyTarget).(*string)
	if ok {
		*targetIdentifer = t.Target()
	}

	tw := newTargetResponseWriter(w, inflightRequest)
	t.proxyHandler.ServeHTTP(tw, req)
}

func (t *Target) Rewrite(req *httputil.ProxyRequest) {
	// Preserve & append X-Forwarded-For
	req.Out.Header["X-Forwarded-For"] = req.In.Header["X-Forwarded-For"]
	req.SetXForwarded()

	req.SetURL(t.targetURL)
	req.Out.Host = req.In.Host

	// Ensure query params are preserved exactly, including those we could not
	// parse.
	//
	// By default, httputil.ReverseProxy will drop unparseable query params to
	// guard against parameter smuggling attacks
	// (https://github.com/golang/go/issues/54663).
	//
	// One example of this is the use of semicolons in query params. Given a URL
	// like:
	//
	//   /path?p=a;b
	//
	// Some platforms interpret these params as equivalent to `p=a` and `b=`,
	// while others interpret it as a single query param: `p=a;b`. Because of this
	// confusion, Go's default behaviour is to drop the parameter entirely,
	// effectively turning our URL into just `/path`.
	//
	// However, any changes to the query params could break applications that
	// depend on them, so we should avoid doing this, and strive to be as
	// transparent as possible.
	//
	// In our case, we don't make any decisions based on the query params, so it's
	// safe for us to pass them through verbatim.
	req.Out.URL.RawQuery = req.In.URL.RawQuery
}

func (t *Target) Drain(timeout time.Duration) {
	originalState := t.updateState(TargetStateDraining)
	if originalState == TargetStateDraining {
		return
	}
	defer t.updateState(originalState)

	deadline := time.After(timeout)
	toCancel := t.pendingRequestsToCancel()

	for _, inflight := range toCancel {
		if inflight.hijacked {
			inflight.cancel()
		}
	}

WAIT_FOR_REQUESTS_TO_COMPLETE:
	for req := range toCancel {
		select {
		case <-req.Context().Done():
		case <-deadline:
			break WAIT_FOR_REQUESTS_TO_COMPLETE
		}
	}

	for _, inflight := range toCancel {
		inflight.cancel()
	}
}

func (t *Target) BeginHealthChecks() {
	t.becameHealthy = make(chan bool)
	t.healthcheck = NewHealthCheck(t,
		t.targetURL.JoinPath(t.healthCheckConfig.Path),
		t.healthCheckConfig.Interval,
		t.healthCheckConfig.Timeout,
	)
}

func (t *Target) StopHealthChecks() {
	if t.healthcheck != nil {
		t.healthcheck.Close()
		t.healthcheck = nil
	}
}

func (t *Target) WaitUntilHealthy(timeout time.Duration) bool {
	select {
	case <-time.After(timeout):
		return false
	case <-t.becameHealthy:
		return true
	}
}

// HealthCheckConsumer

func (t *Target) HealthCheckCompleted(success bool) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	if success && t.state == TargetStateAdding {
		t.state = TargetStateHealthy
		close(t.becameHealthy)
	}

	slog.Info("Target health updated", "target", t.Target(), "success", success, "state", t.state.String())
}

// Private

func (t *Target) createProxyHandler() http.Handler {
	var handler http.Handler

	handler = &httputil.ReverseProxy{
		Rewrite:      t.Rewrite,
		ErrorHandler: t.handleProxyError,
		Transport: &http.Transport{
			MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
			ResponseHeaderTimeout: t.options.TargetTimeout,
		},
	}

	if t.options.MaxRequestBodySize > 0 {
		handler = http.MaxBytesHandler(handler, t.options.MaxRequestBodySize)
		slog.Debug("Using max request body limit", "target", t.Target(), "size", t.options.MaxRequestBodySize)
	}

	return handler
}

func (s *Target) createCertManager() *autocert.Manager {
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

func (t *Target) handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, context.Canceled) {
		slog.Error("Error while proxying", "target", t.Target(), "path", r.URL.Path, "error", err)
	}

	if t.isRequestEntityTooLarge(err) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	if t.isGatewayTimeout(err) {
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}
	w.WriteHeader(http.StatusBadGateway)
}

func (t *Target) isRequestEntityTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func (t *Target) isGatewayTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

func (t *Target) updateState(state TargetState) TargetState {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	originalState := t.state
	t.state = state

	return originalState
}

func (t *Target) beginInflightRequest(req *http.Request) (*http.Request, *inflightRequest) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	if t.state == TargetStateDraining {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	inflightRequest := &inflightRequest{cancel: cancel}
	t.inflight[req] = inflightRequest
	return req, inflightRequest
}

func (t *Target) endInflightRequest(req *http.Request) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	delete(t.inflight, req)
}

func (t *Target) pendingRequestsToCancel() inflightMap {
	// We use a copy of the inflight map to iterate over while draining, so that
	// we don't need to lock it the whole time, which could interfere with the
	// locking that happens when requests end.
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	result := t.inflight
	t.inflight = inflightMap{}
	return result
}

func (t *Target) redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "close")

	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}

	url := "https://" + host + r.URL.RequestURI()
	http.Redirect(w, r, url, http.StatusMovedPermanently)
}

func parseTargetURL(targetURL string) (*url.URL, error) {
	if !hostRegex.MatchString(targetURL) {
		return nil, fmt.Errorf("%s :%w", targetURL, ErrorInvalidHostPattern)
	}

	uri, _ := url.Parse("http://" + targetURL)
	return uri, nil
}

type targetResponseWriter struct {
	http.ResponseWriter
	inflightRequest *inflightRequest
}

func newTargetResponseWriter(w http.ResponseWriter, inflightRequest *inflightRequest) *targetResponseWriter {
	return &targetResponseWriter{w, inflightRequest}
}

func (r *targetResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}

	r.inflightRequest.hijacked = true
	return hijacker.Hijack()
}
