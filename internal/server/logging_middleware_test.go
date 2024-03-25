package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_LoggingMiddleware(t *testing.T) {
	out := &strings.Builder{}
	middleware := NewLoggingMiddleware(out, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, "goodbye")
	}))

	req := httptest.NewRequest("POST", "http://example.com/somepath?q=ok", bytes.NewReader([]byte("hello")))
	req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.1.1.1")
	req.Header.Set("User-Agent", "Robot/1")
	req.Header.Set("Content-Type", "application/json")

	middleware.ServeHTTP(httptest.NewRecorder(), req)

	var logline LoggingMiddlewareLine

	err := json.NewDecoder(strings.NewReader(out.String())).Decode(&logline)
	require.NoError(t, err)

	assert.Equal(t, "Request", logline.Message)
	assert.Equal(t, "INFO", logline.Log.Level)

	assert.Equal(t, "http", logline.URL.Scheme)
	assert.Equal(t, "example.com", logline.URL.Domain)
	assert.Equal(t, "/somepath", logline.URL.Path)
	assert.Equal(t, "q=ok", logline.URL.Query)
	assert.Equal(t, "Robot/1", logline.UserAgent.Original)

	assert.Equal(t, "192.168.1.1", logline.Client.IP)
	assert.Equal(t, 1234, logline.Client.Port)

	assert.Equal(t, "POST", logline.HTTP.Request.Method)
	assert.Equal(t, "application/json", logline.HTTP.Request.MimeType)
	assert.Equal(t, int64(5), logline.HTTP.Request.Body.Bytes)

	assert.Equal(t, http.StatusCreated, logline.HTTP.Response.StatusCode)
	assert.Equal(t, "text/html", logline.HTTP.Response.MimeType)
	assert.Equal(t, int64(8), logline.HTTP.Response.Body.Bytes)
}
