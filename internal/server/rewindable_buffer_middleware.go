package server

import (
	"net/http"
)

type RewindableBufferMiddleware struct {
	maxMemBytes int64
	maxBytes    int64
	next        http.Handler
}

func WithRewindableBufferMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &RewindableBufferMiddleware{
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func (h *RewindableBufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc := NewRewindableReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	defer rc.Dispose()

	r.Body = rc
	h.next.ServeHTTP(w, r)
}
