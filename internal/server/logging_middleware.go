package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

type contextKey string

var (
	contextKeyService = contextKey("service")
	contextKeyTarget  = contextKey("target")
	contextKeyHeaders = contextKey("headers")
)

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

	var service string
	var target string
	var headers = []string{}
	ctx := context.WithValue(r.Context(), contextKeyService, &service)
	ctx = context.WithValue(ctx, contextKeyTarget, &target)
	ctx = context.WithValue(ctx, contextKeyHeaders, &headers)
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

	loggedFields := h.buildHeaderFields(headers, r)
	loggedFields = append(loggedFields,
		slog.String("host", r.Host),
		slog.String("path", r.URL.Path),
		slog.String("request_id", requestID),
		slog.Int("status", writer.statusCode),
		slog.String("service", service),
		slog.String("target", target),
		slog.Int64("duration", elapsed.Nanoseconds()),
		slog.String("method", r.Method),
		slog.Int64("req_content_length", r.ContentLength),
		slog.String("req_content_type", reqContent),
		slog.Int64("resp_content_length", writer.bytesWritten),
		slog.String("resp_content_type", respContent),
		slog.String("remote_addr", remoteAddr),
		slog.String("user_agent", userAgent),
		slog.String("query", r.URL.RawQuery),
	)

	h.logger.LogAttrs(nil, slog.LevelInfo, "Request", loggedFields...)
}

func (h *LoggingMiddleware) buildHeaderFields(headers []string, r *http.Request) []slog.Attr {
	attrs := []slog.Attr{}
	for _, name := range headers {
		key := "header_" + strings.Replace(strings.ToLower(name), "-", "_", -1)
		value := r.Header.Get(name)
		attrs = append(attrs, slog.String(key, value))
	}
	return attrs
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
