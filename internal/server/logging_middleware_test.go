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
	type LogLine struct {
		Timestamp              string `json:"@timestamp"`
		Message                string `json:"message"`
		ClientIP               string `json:"client.ip"`
		ClientPort             int    `json:"client.port"`
		LogLevel               string `json:"log.level"`
		EventDataset           string `json:"event.dataset"`
		EventDuration          int64  `json:"event.duration"`
		DestinationAddress     string `json:"destination.address"`
		HTTPRequestMethod      string `json:"http.request.method"`
		HTTPRequestMimeType    string `json:"http.request.mime_type"`
		HTTPRequestBodyBytes   int64  `json:"http.request.body.bytes"`
		HTTPResponseStatusCode int    `json:"http.response.status_code"`
		HTTPResponseMimeType   string `json:"http.response.mime_type"`
		HTTPResponseBodyBytes  int64  `json:"http.response.body.bytes"`
		SourceDomain           string `json:"source.domain"`
		URLPath                string `json:"url.path"`
		URLQuery               string `json:"url.query"`
		URLScheme              string `json:"url.scheme"`
		UserAgentOriginal      string `json:"user_agent.original"`
	}

	out := &strings.Builder{}
	logger := CreateECSLogger(slog.LevelInfo, out)
	middleware := NewLoggingMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record a value for the `target` context key.
		target, ok := r.Context().Value(contextKeyTarget).(*string)
		if ok {
			*target = "upstream:3000"
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, "goodbye")
	}))

	req := httptest.NewRequest("POST", "http://example.com/somepath?q=ok", bytes.NewReader([]byte("hello")))
	req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.1.1.1")
	req.Header.Set("User-Agent", "Robot/1")
	req.Header.Set("Content-Type", "application/json")

	middleware.ServeHTTP(httptest.NewRecorder(), req)

	var logline LogLine

	err := json.NewDecoder(strings.NewReader(out.String())).Decode(&logline)
	require.NoError(t, err)

	assert.Equal(t, "Request", logline.Message)
	assert.Equal(t, "INFO", logline.LogLevel)
	assert.Equal(t, "upstream:3000", logline.DestinationAddress)
	assert.Equal(t, "proxy.requests", logline.EventDataset)

	assert.Equal(t, "example.com", logline.SourceDomain)
	assert.Equal(t, "http", logline.URLScheme)
	assert.Equal(t, "/somepath", logline.URLPath)
	assert.Equal(t, "q=ok", logline.URLQuery)
	assert.Equal(t, "Robot/1", logline.UserAgentOriginal)

	assert.Equal(t, "192.168.1.1", logline.ClientIP)
	assert.Equal(t, 1234, logline.ClientPort)

	assert.Equal(t, "POST", logline.HTTPRequestMethod)
	assert.Equal(t, "application/json", logline.HTTPRequestMimeType)
	assert.Equal(t, int64(5), logline.HTTPRequestBodyBytes)

	assert.Equal(t, http.StatusCreated, logline.HTTPResponseStatusCode)
	assert.Equal(t, "text/html", logline.HTTPResponseMimeType)
	assert.Equal(t, int64(8), logline.HTTPResponseBodyBytes)
}
