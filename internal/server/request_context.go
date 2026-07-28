package server

import (
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

// RequestContext returns the per-request state shared between middlewares,
// established by RequestContextMiddleware. Requests that did not pass through
// it get a throwaway value, so writes are discarded.
func RequestContext(r *http.Request) *requestContext {
	rc, ok := r.Context().Value(contextKeyRequestContext).(*requestContext)
	if !ok {
		return &requestContext{}
	}
	return rc
}
