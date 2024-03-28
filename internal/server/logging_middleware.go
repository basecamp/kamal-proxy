package server

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type contextKey string

var contextKeyTarget = contextKey("target")

type LoggingMiddleware struct {
	logger *slog.Logger
	next   http.Handler
}

func NewLoggingMiddleware(logger *slog.Logger, next http.Handler) *LoggingMiddleware {
	return &LoggingMiddleware{
		logger: logger,
		next:   next,
	}
}

func (h *LoggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := newResponseWriter(w)

	var target string
	ctx := context.WithValue(r.Context(), contextKeyTarget, &target)
	r = r.WithContext(ctx)

	started := time.Now()
	h.next.ServeHTTP(writer, r)
	elapsed := time.Since(started)

	userAgent := r.Header.Get("User-Agent")
	reqContent := r.Header.Get("Content-Type")
	respContent := writer.Header().Get("Content-Type")

	clientIP, clientPort := h.determineClientIPAndPort(r)

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	h.logger.Info("Request",
		"client.ip", clientIP,
		"client.port", clientPort,
		"log.level", "INFO",
		"destination.address", target,
		"http.request.method", r.Method,
		"http.request.mime_type", reqContent,
		"http.request.body.bytes", r.ContentLength,
		"http.response.status_code", writer.statusCode,
		"http.response.mime_type", respContent,
		"http.response.body.bytes", writer.bytesWritten,
		"source.domain", r.Host,
		"url.path", r.URL.Path,
		"url.query", r.URL.RawQuery,
		"url.scheme", scheme,
		"user_agent.original", userAgent,
		"event.dataset", "proxy.requests",
		"event.duration", elapsed.Nanoseconds(),
	)
}

func (h *LoggingMiddleware) determineClientIPAndPort(r *http.Request) (string, int) {
	ip, portStr, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		portStr = "0"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 0
	}

	forwardedIP := strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]

	if forwardedIP != "" {
		return forwardedIP, port
	}

	return ip, port
}

type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK, 0}
}

// WriteHeader is used to capture the status code
func (r *responseWriter) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Write is used to capture the amount of data written
func (r *responseWriter) Write(b []byte) (int, error) {
	bytesWritten, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(bytesWritten)
	return bytesWritten, err
}

func (r *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
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
