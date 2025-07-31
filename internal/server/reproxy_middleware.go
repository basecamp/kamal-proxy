package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
)

const (
	reproxyLimit             = 5
	reproxyFeatureHeaderName = "X-Kamal-Reproxy"
	reproxyHeaderName        = "X-Kamal-Reproxy-Location"
)

var (
	contextKeyReproxyTo = contextKey("reproxy-to")
)

type ReproxyMiddleware struct {
	serviceName string
	next        http.Handler
}

func WithReproxyMiddleware(serviceName string, next http.Handler) http.Handler {
	return &ReproxyMiddleware{
		serviceName: serviceName,
		next:        next,
	}
}

func (h *ReproxyMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reproxyUrl *url.URL

	for range reproxyLimit {
		ctx := context.WithValue(r.Context(), contextKeyReproxyTo, reproxyUrl)
		req := r.Clone(ctx)                              // Use a fresh request each time
		req.Header.Set(reproxyFeatureHeaderName, "true") // Advertise feature upstream

		if reproxyUrl != nil {
			slog.Info("Reproxying request", "service", h.serviceName, "path", r.URL.Path, "reproxy-to", reproxyUrl.String())
			req.URL = reproxyUrl
		}

		rw := newReproxyResponseWriter(w)
		h.next.ServeHTTP(rw, req)

		if rw.reproxyUrl != nil {
			rewinder, ok := req.Body.(Rewindable)
			if ok {
				rewinder.Rewind()
			} else {
				slog.Error("Unabled to reproxy: body does not support rewinding", "service", h.serviceName, "path", r.URL.Path)
				SetErrorResponse(w, r, http.StatusInternalServerError, nil)
				return
			}

			reproxyUrl = rw.reproxyUrl
		} else {
			return // No more reproxy rexponses to process
		}
	}

	slog.Warn("Exceeded reproxy limit", "service", h.serviceName, "path", r.URL.Path)
	SetErrorResponse(w, r, http.StatusServiceUnavailable, nil)
}

// Private

type reproxyResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	header        http.Header
	headerWritten bool
	reproxyUrl    *url.URL
}

func newReproxyResponseWriter(w http.ResponseWriter) *reproxyResponseWriter {
	return &reproxyResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		header:         http.Header{},
		headerWritten:  false,
	}
}

func (w *reproxyResponseWriter) Header() http.Header {
	return w.header
}

func (w *reproxyResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.extractReproxyTarget()

	if w.reproxyUrl == nil {
		maps.Copy(w.ResponseWriter.Header(), w.header)
		w.ResponseWriter.WriteHeader(statusCode)
	}
	w.headerWritten = true
}

func (w *reproxyResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}

	if w.reproxyUrl != nil {
		return io.Discard.Write(b) // Discard the response if we'll be reproxying the request
	}

	return w.ResponseWriter.Write(b)
}

func (w *reproxyResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}

	return hijacker.Hijack()
}

func (w *reproxyResponseWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func (w *reproxyResponseWriter) extractReproxyTarget() {
	if w.shouldAttemptReproxy() {
		reproxyTarget := w.Header().Get(reproxyHeaderName)
		if reproxyTarget != "" {
			reproxyTo, err := url.Parse(reproxyTarget)
			if err != nil {
				slog.Warn("Reproxy target is not a valid url; ignoring", "reproxy_to", reproxyTarget)
				return
			}

			w.reproxyUrl = reproxyTo
		}
	}

	w.Header().Del(reproxyHeaderName)
}

func (w *reproxyResponseWriter) shouldAttemptReproxy() bool {
	return w.statusCode > 299
}
