package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	StatusClientClosedRequest = 499
)

var (
	ErrorInvalidHostPattern = errors.New("invalid host pattern")
	ErrorDraining           = errors.New("target is draining")

	hostRegex = regexp.MustCompile(`^(\w[-_.\w+]+)(:\d+)?$`)
)

type TargetState int

const (
	TargetStateAdding TargetState = iota
	TargetStateDraining
	TargetStateHealthy
	TargetStateUnhealthy
)

func (ts TargetState) String() string {
	switch ts {
	case TargetStateAdding:
		return "adding"
	case TargetStateDraining:
		return "draining"
	case TargetStateHealthy:
		return "healthy"
	case TargetStateUnhealthy:
		return "unhealthy"
	}
	return ""
}

type TargetStateConsumer interface {
	TargetStateChanged(*Target)
}

type inflightRequest struct {
	cancel   context.CancelCauseFunc
	hijacked bool
}

type inflightMap map[*http.Request]*inflightRequest

type TargetOptions struct {
	HealthCheckConfig   HealthCheckConfig `json:"health_check_config"`
	ResponseTimeout     time.Duration     `json:"response_timeout"`
	BufferRequests      bool              `json:"buffer_requests"`
	BufferResponses     bool              `json:"buffer_responses"`
	MaxMemoryBufferSize int64             `json:"max_memory_buffer_size"`
	MaxRequestBodySize  int64             `json:"max_request_body_size"`
	MaxResponseBodySize int64             `json:"max_response_body_size"`
	LogRequestHeaders   []string          `json:"log_request_headers"`
	LogResponseHeaders  []string          `json:"log_response_headers"`
	ForwardHeaders      bool              `json:"forward_headers"`
	ScopeCookiePaths    bool              `json:"scope_cookie_paths"`
}

func (to *TargetOptions) IsHealthCheckRequest(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == to.HealthCheckConfig.Path
}

func (to *TargetOptions) canonicalizeLogHeaders() {
	for i, header := range to.LogRequestHeaders {
		to.LogRequestHeaders[i] = http.CanonicalHeaderKey(header)
	}
	for i, header := range to.LogResponseHeaders {
		to.LogResponseHeaders[i] = http.CanonicalHeaderKey(header)
	}
}

type Target struct {
	targetURL    *url.URL
	readonly     bool
	options      TargetOptions
	transport    *http.Transport
	proxyHandler http.Handler

	state        TargetState
	inflight     inflightMap
	inflightLock sync.Mutex

	healthcheck   *HealthCheck
	stateConsumer TargetStateConsumer
}

func NewTarget(targetURL string, options TargetOptions) (*Target, error) {
	uri, err := parseTargetURL(targetURL)
	if err != nil {
		return nil, err
	}

	options.canonicalizeLogHeaders()

	target := &Target{
		targetURL: uri,
		options:   options,

		state:    TargetStateAdding,
		inflight: inflightMap{},
	}

	target.proxyHandler = target.createProxyHandler()

	if options.BufferResponses {
		target.proxyHandler = WithResponseBufferMiddleware(options.MaxMemoryBufferSize, options.MaxResponseBodySize, target.proxyHandler)
	}
	if options.BufferRequests {
		target.proxyHandler = WithRequestBufferMiddleware(options.MaxMemoryBufferSize, options.MaxRequestBodySize, target.proxyHandler)
	}

	return target, nil
}

func NewReadOnlyTarget(targetURL string, options TargetOptions) (*Target, error) {
	target, err := NewTarget(targetURL, options)
	if err == nil {
		target.readonly = true
	}

	return target, err
}

func (t *Target) Address() string {
	return t.targetURL.Host
}

func (t *Target) State() TargetState {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	return t.state
}

func (t *Target) ReadOnly() bool {
	return t.readonly
}

func (t *Target) StartRequest(req *http.Request) (*http.Request, error) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	if t.state == TargetStateDraining {
		return nil, ErrorDraining
	}

	ctx, cancel := context.WithCancelCause(req.Context())
	req = req.WithContext(ctx)

	inflightRequest := &inflightRequest{cancel: cancel}
	t.inflight[req] = inflightRequest

	return req, nil
}

func (t *Target) SendRequest(w http.ResponseWriter, req *http.Request) {
	LoggingRequestContext(req).Target = t.Address()
	LoggingRequestContext(req).RequestHeaders = t.options.LogRequestHeaders
	LoggingRequestContext(req).ResponseHeaders = t.options.LogResponseHeaders

	inflightRequest := t.getInflightRequest(req)
	defer t.endInflightRequest(req)

	tw := newTargetResponseWriter(w, inflightRequest, t.cookieScope(req))
	t.proxyHandler.ServeHTTP(tw, req)
}

func (t *Target) Drain(timeout time.Duration) {
	originalState := t.updateState(TargetStateDraining)
	if originalState == TargetStateDraining {
		return
	}
	defer t.updateState(originalState)

	deadline := time.After(timeout)
	toCancel := t.pendingRequestsToCancel()

	// Cancel any hijacked requests immediately, as they may be long-running.
	for _, inflight := range toCancel {
		if inflight.hijacked {
			inflight.cancel(ErrorDraining)
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

	// Cancel any remaining requests.
	for _, inflight := range toCancel {
		inflight.cancel(ErrorDraining)
	}

	t.transport.CloseIdleConnections()
}

func (t *Target) BeginHealthChecks(stateConsumer TargetStateConsumer) {
	t.stateConsumer = stateConsumer

	t.withInflightLock(func() {
		healthCheckURL := t.buildHealthCheckURL()
		t.healthcheck = NewHealthCheck(t,
			healthCheckURL,
			t.options.HealthCheckConfig.Interval,
			t.options.HealthCheckConfig.Timeout,
			t.options.HealthCheckConfig.Host,
		)
	})
}

func (t *Target) StopHealthChecks() {
	t.withInflightLock(func() {
		if t.healthcheck != nil {
			t.healthcheck.Close()
			t.healthcheck = nil
		}
	})
}

// HealthCheckConsumer

func (t *Target) HealthCheckCompleted(success bool) {
	var previousState, newState TargetState

	t.withInflightLock(func() {
		previousState = t.state

		switch success {
		case true:
			switch t.state {
			case TargetStateAdding:
				t.state = TargetStateHealthy
			default:
				t.state = TargetStateHealthy
			}
		case false:
			switch t.state {
			case TargetStateHealthy:
				t.state = TargetStateUnhealthy
			}
		}

		newState = t.state
	})

	if newState != previousState {
		slog.Info("Target health updated", "target", t.Address(), "state", newState.String(), "was", previousState.String())

		if t.stateConsumer != nil {
			t.stateConsumer.TargetStateChanged(t)
		}
	}
}

// Private

func (t *Target) buildHealthCheckURL() *url.URL {
	healthCheckURL := *t.targetURL

	if t.options.HealthCheckConfig.Port > 0 {
		host, _, err := net.SplitHostPort(t.targetURL.Host)
		if err != nil {
			host = t.targetURL.Host
		}
		healthCheckURL.Host = fmt.Sprintf("%s:%d", host, t.options.HealthCheckConfig.Port)
	}

	return healthCheckURL.JoinPath(t.options.HealthCheckConfig.Path)
}

func (t *Target) createProxyHandler() http.Handler {
	bufferPool := NewBufferPool(ProxyBufferSize)

	t.transport = &http.Transport{
		MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
		ResponseHeaderTimeout: t.options.ResponseTimeout,
	}

	return &httputil.ReverseProxy{
		BufferPool:   bufferPool,
		Rewrite:      t.rewrite,
		ErrorHandler: t.handleProxyError,
		Transport:    t.transport,
	}
}

func (t *Target) rewrite(req *httputil.ProxyRequest) {
	t.forwardHeaders(req)

	req.SetURL(t.targetURL)
	req.Out.Host = req.In.Host

	routingContext := RoutingContext(req.In)
	if routingContext != nil {
		req.Out.URL.Path = strings.TrimPrefix(req.Out.URL.Path, routingContext.MatchedPrefix)
	}

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

func (t *Target) forwardHeaders(req *httputil.ProxyRequest) {
	if t.options.ForwardHeaders {
		req.Out.Header["X-Forwarded-For"] = req.In.Header["X-Forwarded-For"]
	}

	req.SetXForwarded()

	if t.options.ForwardHeaders {
		if req.In.Header.Get("X-Forwarded-Proto") != "" {
			req.Out.Header.Set("X-Forwarded-Proto", req.In.Header.Get("X-Forwarded-Proto"))
		}
		if req.In.Header.Get("X-Forwarded-Host") != "" {
			req.Out.Header.Set("X-Forwarded-Host", req.In.Header.Get("X-Forwarded-Host"))
		}
	}
}

func (t *Target) handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	if t.isRequestEntityTooLarge(err) {
		SetErrorResponse(w, r, http.StatusRequestEntityTooLarge, nil)
		return
	}

	if t.isGatewayTimeout(err) {
		SetErrorResponse(w, r, http.StatusGatewayTimeout, nil)
		return
	}

	if t.isClientCancellation(err) {
		// The client has disconnected so will not see the response, but we
		// still want to set it for the sake of the logs.
		w.WriteHeader(StatusClientClosedRequest)
		return
	}

	if t.isDraining(err) {
		slog.Info("Request cancelled due to draining", "target", t.Address(), "path", r.URL.Path)
		SetErrorResponse(w, r, http.StatusGatewayTimeout, nil)
		return
	}

	if isChunkedEncodingError(err) {
		slog.Info("Malformed request", "target", t.Address(), "path", r.URL.Path, "error", err)
		SetErrorResponse(w, r, http.StatusBadRequest, nil)
		return
	}

	slog.Error("Error while proxying", "target", t.Address(), "path", r.URL.Path, "error", err)
	SetErrorResponse(w, r, http.StatusBadGateway, nil)
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

func (t *Target) isClientCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (t *Target) isDraining(err error) bool {
	return errors.Is(err, ErrorDraining)
}

func (t *Target) updateState(state TargetState) TargetState {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	originalState := t.state
	t.state = state

	return originalState
}

func (t *Target) getInflightRequest(req *http.Request) *inflightRequest {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	return t.inflight[req]
}

func (t *Target) endInflightRequest(req *http.Request) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	inflightRequest, ok := t.inflight[req]
	if ok {
		inflightRequest.cancel(nil)
		delete(t.inflight, req)
	}
}

func (t *Target) pendingRequestsToCancel() inflightMap {
	// We use a copy of the inflight map to iterate over while draining, so that
	// we don't need to lock it the whole time, which could interfere with the
	// locking that happens when requests end.
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	result := inflightMap{}
	maps.Copy(result, t.inflight)
	return result
}

func (t *Target) cookieScope(r *http.Request) *CookieScope {
	if !t.options.ScopeCookiePaths {
		return nil
	}

	routingContext := RoutingContext(r)
	if routingContext == nil || routingContext.MatchedPrefix == "" {
		return nil
	}

	return NewCookieScope(routingContext.MatchedPrefix, r.Host)
}

func (t *Target) withInflightLock(fn func()) {
	t.inflightLock.Lock()
	defer t.inflightLock.Unlock()

	fn()
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
	header          http.Header
	headerWritten   bool
	inflightRequest *inflightRequest
	cookieScope     *CookieScope
}

func newTargetResponseWriter(w http.ResponseWriter, inflightRequest *inflightRequest, cookieScope *CookieScope) *targetResponseWriter {
	return &targetResponseWriter{
		ResponseWriter:  w,
		header:          http.Header{},
		headerWritten:   false,
		inflightRequest: inflightRequest,
		cookieScope:     cookieScope,
	}
}

func (w *targetResponseWriter) Header() http.Header {
	return w.header
}

func (w *targetResponseWriter) WriteHeader(statusCode int) {
	if w.cookieScope != nil {
		w.cookieScope.ApplyToHeader(w.header)
	}
	maps.Copy(w.ResponseWriter.Header(), w.header)

	w.ResponseWriter.WriteHeader(statusCode)
	w.headerWritten = true
}

func (w *targetResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(b)
}

func (w *targetResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}

	w.inflightRequest.hijacked = true
	return hijacker.Hijack()
}

func (w *targetResponseWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
