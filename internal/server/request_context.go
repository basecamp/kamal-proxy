package server

import (
	"context"
	"net/http"
)

type contextKey string

var contextKeyRequestContext = contextKey("request-context")

type requestContext struct {
	Service         string
	Target          string
	ClientIP        string
	RequestHeaders  []string
	ResponseHeaders []string
	ExcludeMetrics  bool
}

// RequestContext returns the per-request state shared between middlewares.
// LoggingMiddleware establishes it, so anything that writes to it must run
// inside LoggingMiddleware; for requests that did not pass through it, writes
// go to a throwaway value.
func RequestContext(r *http.Request) *requestContext {
	rc, ok := r.Context().Value(contextKeyRequestContext).(*requestContext)
	if !ok {
		return &requestContext{}
	}
	return rc
}

func withRequestContext(r *http.Request) (*http.Request, *requestContext) {
	rc := &requestContext{}
	ctx := context.WithValue(r.Context(), contextKeyRequestContext, rc)
	return r.WithContext(ctx), rc
}
