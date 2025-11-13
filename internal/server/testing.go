package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	defaultHealthCheckConfig  = HealthCheckConfig{Path: DefaultHealthCheckPath, Port: DefaultHealthCheckPort, Interval: DefaultHealthCheckInterval, Timeout: time.Second * 5}
	defaultEmptyReaders       = []string{}
	defaultServiceOptions     = ServiceOptions{TLSRedirect: true}
	defaultTargetOptions      = TargetOptions{HealthCheckConfig: defaultHealthCheckConfig, ResponseTimeout: DefaultTargetTimeout}
	defaultDeploymentOptions  = DeploymentOptions{DeployTimeout: DefaultDeployTimeout, DrainTimeout: DefaultDrainTimeout, Force: false}
)

func testTarget(t testing.TB, handler http.HandlerFunc) *Target {
	t.Helper()

	_, targetURL := testBackendWithHandler(t, handler)

	target, err := NewTarget(targetURL, defaultTargetOptions)
	require.NoError(t, err)
	return target
}

func testReadOnlyTarget(t testing.TB, handler http.HandlerFunc) *Target {
	t.Helper()

	_, targetURL := testBackendWithHandler(t, handler)

	target, err := NewReadOnlyTarget(targetURL, defaultTargetOptions)
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
		w.Write([]byte(body))
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

func testServer(t testing.TB, http3Enabled bool) *Server {
	t.Helper()

	config := &Config{
		Bind:               "127.0.0.1",
		HttpPort:           0,
		HttpsPort:          0,
		AlternateConfigDir: t.TempDir(),
		HTTP3Enabled:       http3Enabled,
	}
	router := NewRouter(config.StatePath())
	server := NewServer(config, router)
	err := server.Start()
	require.NoError(t, err)

	t.Cleanup(server.Stop)

	return server
}
