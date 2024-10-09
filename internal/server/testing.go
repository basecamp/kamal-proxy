package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	defaultHealthCheckConfig = HealthCheckConfig{Path: DefaultHealthCheckPath, Interval: DefaultHealthCheckInterval, Timeout: DefaultHealthCheckTimeout}
	defaultEmptyHosts        = []string{}
	defaultServiceOptions    = ServiceOptions{}
	defaultTargetOptions     = TargetOptions{HealthCheckConfig: defaultHealthCheckConfig, ResponseTimeout: DefaultTargetTimeout}
)

func testTarget(t testing.TB, handler http.HandlerFunc) *Target {
	t.Helper()

	_, targetURL := testBackendWithHandler(t, handler)

	target, err := NewTarget(targetURL, defaultTargetOptions)
	require.NoError(t, err)
	return target
}

func testTargetWithOptions(t testing.TB, targetOptions TargetOptions, handler http.HandlerFunc) *Target {
	t.Helper()

	_, targetURL := testBackendWithHandler(t, handler)

	target, err := NewTarget(targetURL, targetOptions)
	require.NoError(t, err)
	return target
}

func testBackend(t testing.TB, body string, statusCode int) (*httptest.Server, string) {
	t.Helper()

	return testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, err := w.Write([]byte(body))
		assert.NoError(t, err)
	})
}

func testBackendWithHandler(t testing.TB, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	return server, serverURL.Host
}

func testServer(t testing.TB) (*Server, string) {
	t.Helper()

	config := &Config{
		Bind:               "127.0.0.1",
		HttpPort:           0,
		HttpsPort:          0,
		AlternateConfigDir: t.TempDir(),
	}
	router := NewRouter(config.StatePath())
	server := NewServer(config, router)
	err := server.Start()
	require.NoError(t, err)
	t.Cleanup(server.Stop)

	addr := fmt.Sprintf("http://localhost:%d", server.HttpPort())

	return server, addr
}
