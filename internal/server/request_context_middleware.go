package server

import (
	"context"
	"net/http"
)

type RequestContextMiddleware struct {
	next http.Handler
}

func WithRequestContextMiddleware(next http.Handler) http.Handler {
	return &RequestContextMiddleware{
		next: next,
	}
}

func (h *RequestContextMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithValue(r.Context(), contextKeyRequestContext, &requestContext{})
	h.next.ServeHTTP(w, r.WithContext(ctx))
}
