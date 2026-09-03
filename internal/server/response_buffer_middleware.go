package server

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
)

type ResponseBufferMiddleware struct {
	maxMemBytes int64
	maxBytes    int64
	next        http.Handler
}

func WithResponseBufferMiddleware(maxMemBytes, maxBytes int64, next http.Handler) http.Handler {
	return &ResponseBufferMiddleware{
		maxMemBytes: maxMemBytes,
		maxBytes:    maxBytes,
		next:        next,
	}
}

func (h *ResponseBufferMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	responseBuffer := NewBufferedWriteCloser(h.maxBytes, h.maxMemBytes)
	responseWriter := &bufferedResponseWriter{ResponseWriter: w, statusCode: http.StatusOK, buffer: responseBuffer}
	defer responseBuffer.Close()

	h.next.ServeHTTP(responseWriter, r)

	err := responseWriter.Send()
	if err != nil {
		if errors.Is(err, ErrMaximumSizeExceeded) {
			slog.Info("Response exceeded max response limit", "path", r.URL.Path)
			SetErrorResponse(w, r, http.StatusInternalServerError, nil)
		} else {
			slog.Error("Error sending response", "path", r.URL.Path, "error", err)
			SetErrorResponse(w, r, http.StatusInternalServerError, nil)
		}
		return
	}
}

// isInformational reports whether statusCode is a 1xx interim response. 101
// Switching Protocols is excluded: it ends the HTTP exchange, so it is final.
func isInformational(statusCode int) bool {
	return statusCode >= 100 && statusCode < 200 && statusCode != http.StatusSwitchingProtocols
}

type bufferedResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	buffer        *Buffer
	hijacked      bool
	headerWritten bool
	bypass        bool
}

func (w *bufferedResponseWriter) Send() error {
	if w.buffer.Overflowed() {
		return ErrMaximumSizeExceeded
	}

	if w.hijacked || w.bypass {
		return nil
	}

	if w.headerWritten {
		w.ResponseWriter.WriteHeader(w.statusCode)
	}

	return w.buffer.Send(w.ResponseWriter)
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *bufferedResponseWriter) WriteHeader(statusCode int) {
	if isInformational(statusCode) {
		// 1xx responses precede the final response rather than replace it.
		// httputil.ReverseProxy relays them from the upstream, for example the
		// 100 Continue a backend sends when the client used Expect:
		// 100-continue. Pass them straight through and keep waiting for the
		// real status, otherwise we'd latch the 1xx as the final one and the
		// client would get an implicit 200 instead.
		w.ResponseWriter.WriteHeader(statusCode)
		return
	}

	if !w.headerWritten {
		w.statusCode = statusCode
		w.headerWritten = true

		if w.ShouldSwitchToUnbuffered() {
			w.SwitchToUnbuffered()
		}
	}
}

func (w *bufferedResponseWriter) ShouldSwitchToUnbuffered() bool {
	// Check for explicit streaming content types
	contentType, _, _ := strings.Cut(w.Header().Get("Content-Type"), ";")
	if contentType == "text/event-stream" {
		return true
	}

	// Check for chunked transfer encoding - RFC 7230 indicates this is for streaming
	if w.Header().Get("Transfer-Encoding") == "chunked" {
		return true
	}

	return false
}

func (w *bufferedResponseWriter) SwitchToUnbuffered() {
	_ = w.Send()
	w.bypass = true
}

func (w *bufferedResponseWriter) Write(data []byte) (int, error) {
	if w.bypass {
		return w.ResponseWriter.Write(data)
	}

	n, err := w.buffer.Write(data)
	if errors.Is(err, ErrMaximumSizeExceeded) {
		// Returning an error here will cause the ReverseProxy to panic. If the
		// error is that we're exceeding the limit, just pretend it was all
		// fine. We'll handle the overflow condition when we send the buffer to
		// the client.
		return len(data), nil
	}

	return n, err
}

func (w *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.hijacked = true
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *bufferedResponseWriter) Flush() {
	if w.bypass {
		flusher, ok := w.ResponseWriter.(http.Flusher)
		if ok {
			flusher.Flush()
		}
	}
}
