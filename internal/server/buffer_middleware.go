package server

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
)

type BufferMiddleware struct {
	maxMemBytes      int64
	maxRequestBytes  int64
	maxResponseBytes int64
	next             http.Handler
}

func WithBufferMiddleware(maxMemBytes, maxRequestBytes, maxResponseBytes int64, next http.Handler) http.Handler {
	return &BufferMiddleware{
		maxMemBytes:      maxMemBytes,
		maxRequestBytes:  maxRequestBytes,
		maxResponseBytes: maxResponseBytes,
		next:             next,
	}
}

func (h *BufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestBuffer, err := NewBufferedReadCloser(r.Body, h.maxRequestBytes, h.maxMemBytes)
	if err != nil {
		if err == ErrMaximumSizeExceeded {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			slog.Error("Error buffering request", "path", r.URL.Path, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	responseBuffer := NewBufferedWriteCloser(h.maxResponseBytes, h.maxMemBytes)
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
