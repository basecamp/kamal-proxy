package server

import (
	"net/http"
)

type ClientIPMiddleware struct {
	headerName string
	next       http.Handler
}

func WithClientIPMiddleware(headerName string, next http.Handler) http.Handler {
	return &ClientIPMiddleware{
		headerName: http.CanonicalHeaderKey(headerName),
		next:       next,
	}
}

func (h *ClientIPMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if clientIP := r.Header.Get(h.headerName); clientIP != "" {
		r.Header.Set("X-Forwarded-For", clientIP)
	}
	h.next.ServeHTTP(w, r)
}
