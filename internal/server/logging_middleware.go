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

	"github.com/basecamp/kamal-proxy/internal/metrics"
)

type contextKey string

var contextKeyRequestContext = contextKey("request-context")

type loggingRequestContext struct {
	Service         string
	Target          string
	RequestHeaders  []string
	ResponseHeaders []string
}

type LoggingMiddleware struct {
	logger    *slog.Logger
	httpPort  int
	httpsPort int
	next      http.Handler
}

func WithLoggingMiddleware(logger *slog.Logger, httpPort, httpsPort int, next http.Handler) http.Handler {
	return &LoggingMiddleware{
		logger:    logger,
		httpPort:  httpPort,
		httpsPort: httpsPort,
		next:      next,
	}
}

func LoggingRequestContext(r *http.Request) *loggingRequestContext {
	lrc, ok := r.Context().Value(contextKeyRequestContext).(*loggingRequestContext)
	if !ok {
		return &loggingRequestContext{}
	}
	return lrc
}

func (h *LoggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writer := newLoggerResponseWriter(w)

	var loggingRequestContext loggingRequestContext
	ctx := context.WithValue(r.Context(), contextKeyRequestContext, &loggingRequestContext)
	r = r.WithContext(ctx)

	started := time.Now()

	defer func() {
		elapsed := time.Since(started)

		port := h.httpPort
		scheme := "http"
		if r.TLS != nil {
			port = h.httpsPort
			scheme = "https"
		}

		clientAddr, clientPort, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			clientAddr = r.RemoteAddr
			clientPort = ""
		}

		remoteAddr := r.Header.Get("X-Forwarded-For")
		if remoteAddr == "" {
			remoteAddr = clientAddr
		}

		attrs := []slog.Attr{
			slog.String("host", r.Host),
			slog.Int("port", port),
			slog.String("path", r.URL.Path),
			slog.String("request_id", r.Header.Get("X-Request-ID")),
			slog.Int("status", writer.statusCode),
			slog.String("service", loggingRequestContext.Service),
			slog.String("target", loggingRequestContext.Target),
			slog.Int64("duration", elapsed.Nanoseconds()),
			slog.String("method", r.Method),
			slog.Int64("req_content_length", r.ContentLength),
			slog.String("req_content_type", r.Header.Get("Content-Type")),
			slog.Int64("resp_content_length", writer.bytesWritten),
			slog.String("resp_content_type", writer.Header().Get("Content-Type")),
			slog.String("client_addr", clientAddr),
			slog.String("client_port", clientPort),
			slog.String("remote_addr", remoteAddr),
			slog.String("user_agent", r.Header.Get("User-Agent")),
			slog.String("proto", r.Proto),
			slog.String("scheme", scheme),
			slog.String("query", r.URL.RawQuery),
		}

		attrs = append(attrs, h.retrieveCustomHeaders(loggingRequestContext.RequestHeaders, r.Header, "req")...)
		attrs = append(attrs, h.retrieveCustomHeaders(loggingRequestContext.ResponseHeaders, writer.Header(), "resp")...)
		h.logger.LogAttrs(context.Background(), slog.LevelInfo, "Request", attrs...)

		metrics.Tracker.TrackRequest(loggingRequestContext.Service, r.Method, writer.statusCode, elapsed)
	}()

	h.next.ServeHTTP(writer, r)
}

func (h *LoggingMiddleware) retrieveCustomHeaders(headerNames []string, header http.Header, prefix string) []slog.Attr {
	attrs := []slog.Attr{}
	for _, headerName := range headerNames {
		name := prefix + "_" + strings.ReplaceAll(strings.ToLower(headerName), "-", "_")
		value := strings.Join(header[headerName], ",")
		attrs = append(attrs, slog.String(name, value))
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

func (r *loggerResponseWriter) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}
