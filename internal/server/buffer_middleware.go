package server

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
)

type BufferMiddleware struct {
	maxRequestBytes     int64
	maxRequestMemBytes  int64
	maxResponseBytes    int64
	maxResponseMemBytes int64
	next                http.Handler
}

func WithBufferMiddleware(maxRequestBytes, maxRequestMemBytes, maxResponseBytes, maxResponseMemBytes int64, next http.Handler) http.Handler {
	return &BufferMiddleware{
		maxRequestBytes:     maxRequestBytes,
		maxRequestMemBytes:  maxRequestMemBytes,
		maxResponseBytes:    maxResponseBytes,
		maxResponseMemBytes: maxResponseMemBytes,
		next:                next,
	}
}

func (h *BufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestBuffer, err := NewBufferedReadCloser(r.Body, h.maxRequestBytes, h.maxRequestMemBytes)
	if err != nil {
		if err == ErrMaximumSizeExceeded {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	responseBuffer := NewBufferedWriteCloser(h.maxResponseBytes, h.maxResponseMemBytes)
	responseWriter := &bufferedResponseWriter{ResponseWriter: w, statusCode: http.StatusOK, buffer: responseBuffer}
	defer responseBuffer.Close()

	r.Body = requestBuffer
	h.next.ServeHTTP(responseWriter, r)

	err = responseWriter.Send()
	if err != nil {
		slog.Error("Error sending response", "path", r.URL.Path, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

type bufferedResponseWriter struct {
	http.ResponseWriter
	statusCode int
	buffer     *Buffer
	hijacked   bool
}

func (w *bufferedResponseWriter) Send() error {
	if w.buffer.Overflowed() {
		return ErrMaximumSizeExceeded
	}

	if w.hijacked {
		return nil
	}

	w.ResponseWriter.WriteHeader(w.statusCode)
	return w.buffer.Send(w.ResponseWriter)
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *bufferedResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	return w.buffer.Write(data)
}

func (w *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.hijacked = true
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
