package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

var defaultHealthCheckConfig = HealthCheckConfig{
	Path:     DefaultHealthCheckPath,
	Interval: DefaultHealthCheckInterval,
	Timeout:  DefaultHealthCheckTimeout,
}

var defaultTargetOptions = TargetOptions{
	RequireTLS:         false,
	MaxRequestBodySize: 0,
}

func testBackend(t *testing.T, body string, statusCode int) (*httptest.Server, *Target) {
	return testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	})
}

func testBackendWithHandler(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Target) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, defaultTargetOptions)
	require.NoError(t, err)

	return server, target
}
