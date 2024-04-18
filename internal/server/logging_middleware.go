package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type contextKey string

var contextKeyTarget = contextKey("target")

type LoggingMiddleware struct {
	logger *slog.Logger
	next   http.Handler
}

func WithLoggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return &LoggingMiddleware{
		logger: logger,
		next:   next,
	}
}

func (h *LoggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := newLoggerResponseWriter(w)

	var target string
	ctx := context.WithValue(r.Context(), contextKeyTarget, &target)
	r = r.WithContext(ctx)

	started := time.Now()
	h.next.ServeHTTP(writer, r)
	elapsed := time.Since(started)

	userAgent := r.Header.Get("User-Agent")
	reqContent := r.Header.Get("Content-Type")
	respContent := writer.Header().Get("Content-Type")
	remoteAddr := r.Header.Get("X-Forwarded-For")
	requestID := r.Header.Get("X-Request-ID")
	if remoteAddr == "" {
		remoteAddr = r.RemoteAddr
	}

	h.logger.Info("Request",
		"host", r.Host,
		"path", r.URL.Path,
		"request_id", requestID,
		"status", writer.statusCode,
		"target", target,
		"duration", elapsed.Nanoseconds(),
		"method", r.Method,
		"req_content_length", r.ContentLength,
		"req_content_type", reqContent,
		"resp_content_length", writer.bytesWritten,
		"resp_content_type", respContent,
		"remote_addr", remoteAddr,
		"user_agent", userAgent,
		"query", r.URL.RawQuery)
}

type loggerResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func newLoggerResponseWriter(w http.ResponseWriter) *loggerResponseWriter {
	return &loggerResponseWriter{w, http.StatusOK, 0}
}

// WriteHeader is used to capture the status code
func (r *loggerResponseWriter) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Write is used to capture the amount of data written
func (r *loggerResponseWriter) Write(b []byte) (int, error) {
	bytesWritten, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(bytesWritten)
	return bytesWritten, err
}

func (r *loggerResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("ResponseWriter does not implement http.Hijacker")
	}

	con, rw, err := hijacker.Hijack()
	if err == nil {
		r.statusCode = http.StatusSwitchingProtocols
	}
	return con, rw, err
}
