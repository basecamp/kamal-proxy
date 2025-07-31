package server

import (
	"io"
	"log/slog"
	"net/http"
)

type RequestBufferMiddleware struct {
	buffered    bool
	maxMemBytes int64
	maxBytes    int64
	next        http.Handler
}

func WithRequestBufferMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &RequestBufferMiddleware{
		buffered:    true,
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func WithRewindableRequestMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &RequestBufferMiddleware{
		buffered:    false,
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func (h *RequestBufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var requestBuffer io.ReadCloser
	var err error

	if h.buffered {
		requestBuffer, err = NewBufferedReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	} else {
		requestBuffer, err = NewRewindableReadCloser(r.Body, h.maxBytes, h.maxMemBytes)
	}

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
