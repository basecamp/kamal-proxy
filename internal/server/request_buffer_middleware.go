package server

import (
	"errors"
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
		if errors.Is(err, ErrMaximumSizeExceeded) {
			SetErrorResponse(w, r, http.StatusRequestEntityTooLarge, nil)
		} else if isChunkedEncodingError(err) {
			slog.Info("Malformed chunked request", "path", r.URL.Path, "error", err)
			SetErrorResponse(w, r, http.StatusBadRequest, nil)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			SetErrorResponse(w, r, http.StatusInternalServerError, nil)
		}
		return
	}

	r.Body = requestBuffer
	h.next.ServeHTTP(w, r)
}
