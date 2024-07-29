package server

import (
	"log/slog"
	"net/http"
)

type RequestBufferMiddleware struct {
	maxMemBytes int64
	maxBytes    int64
	next        http.Handler
}

func WithRequestBufferMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &RequestBufferMiddleware{
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func (h *RequestBufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestBuffer, err := NewBufferedReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	if err != nil {
		if err == ErrMaximumSizeExceeded {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	r.Body = requestBuffer
	h.next.ServeHTTP(w, r)
}
