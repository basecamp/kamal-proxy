package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_LoggingMiddleware(t *testing.T) {
	out := &strings.Builder{}
	logger := slog.New(slog.NewJSONHandler(out, nil))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		LoggingRequestContext(r).Service = "myapp"
		LoggingRequestContext(r).Target = "upstream:3000"
		LoggingRequestContext(r).RequestHeaders = []string{"X-Custom"}
		LoggingRequestContext(r).ResponseHeaders = []string{"Cache-Control", "X-Custom"}

		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Custom", "goodbye")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, "goodbye")
	})

	middleware := WithLoggingMiddleware(logger, 80, 443, handler)

	req := httptest.NewRequest("POST", "http://app.example.com/somepath?q=ok", bytes.NewReader([]byte("hello")))
	req.Header.Set("X-Request-ID", "request-id")
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	req.Header.Set("User-Agent", "Robot/1")
	req.Header.Set("Content-Type", "application/json")

	// Ensure non-canonicalised headers are logged too
	req.Header.Set("x-custom", "hello")

	middleware.ServeHTTP(httptest.NewRecorder(), req)

	logline := struct {
		Message           string `json:"msg"`
		Level             string `json:"level"`
		RequestID         string `json:"request_id"`
		Host              string `json:"host"`
		Port              int    `json:"port"`
		Path              string `json:"path"`
		Method            string `json:"method"`
		Status            int    `json:"status"`
		ClientAddr        string `json:"client_addr"`
		ClientPort        string `json:"client_port"`
		RemoteAddr        string `json:"remote_addr"`
		UserAgent         string `json:"user_agent"`
		ReqContentLength  int64  `json:"req_content_length"`
		ReqContentType    string `json:"req_content_type"`
		RespContentLength int64  `json:"resp_content_length"`
		RespContentType   string `json:"resp_content_type"`
		Query             string `json:"query"`
		Service           string `json:"service"`
		Target            string `json:"target"`
		ReqXCustom        string `json:"req_x_custom"`
		RespCacheControl  string `json:"resp_cache_control"`
		RespXCustom       string `json:"resp_x_custom"`
		Proto             string `json:"proto"`
		Scheme            string `json:"scheme"`
	}{}

	err := json.NewDecoder(strings.NewReader(out.String())).Decode(&logline)
	require.NoError(t, err)

	assert.Equal(t, "Request", logline.Message)
	assert.Equal(t, "INFO", logline.Level)
	assert.Equal(t, "request-id", logline.RequestID)
	assert.Equal(t, "app.example.com", logline.Host)
	assert.Equal(t, 80, logline.Port)
	assert.Equal(t, "/somepath", logline.Path)
	assert.Equal(t, "POST", logline.Method)
	assert.Equal(t, http.StatusCreated, logline.Status)
	assert.Equal(t, "192.0.2.1", logline.ClientAddr)
	assert.Equal(t, "1234", logline.ClientPort)
	assert.Equal(t, "192.168.1.1", logline.RemoteAddr)
	assert.Equal(t, "Robot/1", logline.UserAgent)
	assert.Equal(t, "application/json", logline.ReqContentType)
	assert.Equal(t, "text/html", logline.RespContentType)
	assert.Equal(t, "q=ok", logline.Query)
	assert.Equal(t, int64(5), logline.ReqContentLength)
	assert.Equal(t, int64(8), logline.RespContentLength)
	assert.Equal(t, "upstream:3000", logline.Target)
	assert.Equal(t, "myapp", logline.Service)
	assert.Equal(t, "hello", logline.ReqXCustom)
	assert.Equal(t, "public, max-age=3600", logline.RespCacheControl)
	assert.Equal(t, "goodbye", logline.RespXCustom)
	assert.Equal(t, "HTTP/1.1", logline.Proto)
	assert.Equal(t, "http", logline.Scheme)
}
