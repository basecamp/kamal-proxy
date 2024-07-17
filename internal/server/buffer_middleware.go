package server

import (
	"log/slog"
	"net/http"
)

type BufferMiddleware struct {
	maxBytes    int64
	maxMemBytes int64
	next        http.Handler
}

func WithBufferMiddleware(maxBytes, maxMemBytes int64, next http.Handler) http.Handler {
	return &BufferMiddleware{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
		next:        next,
	}
}

func (h *BufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buffer, err := NewBufferedReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	if err != nil {
		if err == ErrMaximumSizeExceeded {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	r.Body = buffer
	h.next.ServeHTTP(w, r)
}
