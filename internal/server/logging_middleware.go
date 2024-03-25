package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type contextKey string

var contextKeyTarget = contextKey("target")

type LoggingMiddlewareLine struct {
	Timestamp string `json:"@timestamp"`
	Message   string `json:"message"`
	Client    struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"client"`
	Log struct {
		Level string `json:"level"`
	} `json:"log"`
	Event struct {
		Duration int64 `json:"duration"`
	} `json:"event"`
	Destination struct {
		Address string `json:"address"`
	} `json:"destination"`
	HTTP struct {
		Request struct {
			Method   string `json:"method"`
			MimeType string `json:"mime_type"`
			Body     struct {
				Bytes int64 `json:"bytes"`
			} `json:"body"`
		} `json:"request"`
		Response struct {
			StatusCode int    `json:"status_code"`
			MimeType   string `json:"mime_type"`
			Body       struct {
				Bytes int64 `json:"bytes"`
			} `json:"body"`
		} `json:"response"`
	} `json:"http"`
	URL struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
		Query  string `json:"query"`
		Scheme string `json:"scheme"`
	} `json:"url"`
	UserAgent struct {
		Original string `json:"original"`
	} `json:"user_agent"`
}

type LoggingMiddleware struct {
	encoder *json.Encoder
	next    http.Handler
}

func NewLoggingMiddleware(w io.Writer, next http.Handler) *LoggingMiddleware {
	return &LoggingMiddleware{
		encoder: json.NewEncoder(w),
		next:    next,
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

	line := LoggingMiddlewareLine{
		Timestamp: started.Format(time.RFC3339Nano),
		Message:   "Request",
	}

	line.Log.Level = "INFO"
	line.Client.IP = clientIP
	line.Client.Port = clientPort
	line.Destination.Address = target
	line.Event.Duration = elapsed.Nanoseconds()
	line.HTTP.Request.Body.Bytes = r.ContentLength
	line.HTTP.Request.Method = r.Method
	line.HTTP.Request.MimeType = reqContent
	line.HTTP.Response.Body.Bytes = writer.bytesWritten
	line.HTTP.Response.MimeType = respContent
	line.HTTP.Response.StatusCode = writer.statusCode
	line.URL.Domain = r.Host
	line.URL.Path = r.URL.Path
	line.URL.Query = r.URL.RawQuery
	line.URL.Scheme = scheme

	line.UserAgent.Original = userAgent

	h.encoder.Encode(line)
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
