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
	middleware := NewLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record a value for the `target` context key
		target, ok := r.Context().Value(contextKeyTarget).(*string)
		if ok {
			*target = "upstream:3000"
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, "goodbye")
	}))

	req := httptest.NewRequest("POST", "http://app.example.com/somepath?q=ok", bytes.NewReader([]byte("hello")))
	req.Header.Set("X-Forwarded-For", "192.168.1.1")
	req.Header.Set("User-Agent", "Robot/1")
	req.Header.Set("Content-Type", "application/json")

	middleware.ServeHTTP(httptest.NewRecorder(), req)

	logline := struct {
		Message           string `json:"msg"`
		Level             string `json:"level"`
		Host              string `json:"host"`
		Path              string `json:"path"`
		Method            string `json:"method"`
		Status            int    `json:"status"`
		RemoteAddr        string `json:"remote_addr"`
		UserAgent         string `json:"user_agent"`
		ReqContentLength  int64  `json:"req_content_length"`
		ReqContentType    string `json:"req_content_type"`
		RespContentLength int64  `json:"resp_content_length"`
		RespContentType   string `json:"resp_content_type"`
		Query             string `json:"query"`
		Target            string `json:"target"`
	}{}

	err := json.NewDecoder(strings.NewReader(out.String())).Decode(&logline)
	require.NoError(t, err)

	assert.Equal(t, "Request", logline.Message)
	assert.Equal(t, "INFO", logline.Level)
	assert.Equal(t, "app.example.com", logline.Host)
	assert.Equal(t, "/somepath", logline.Path)
	assert.Equal(t, "POST", logline.Method)
	assert.Equal(t, http.StatusCreated, logline.Status)
	assert.Equal(t, "192.168.1.1", logline.RemoteAddr)
	assert.Equal(t, "Robot/1", logline.UserAgent)
	assert.Equal(t, "application/json", logline.ReqContentType)
	assert.Equal(t, "text/html", logline.RespContentType)
	assert.Equal(t, "q=ok", logline.Query)
	assert.Equal(t, int64(5), logline.ReqContentLength)
	assert.Equal(t, int64(8), logline.RespContentLength)
	assert.Equal(t, "upstream:3000", logline.Target)
}
