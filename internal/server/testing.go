package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var (
	defaultHealthCheckConfig = HealthCheckConfig{Path: DefaultHealthCheckPath, Interval: DefaultHealthCheckInterval, Timeout: DefaultHealthCheckTimeout}
	defaultServiceOptions    = ServiceOptions{HealthCheckConfig: defaultHealthCheckConfig, RequestTimeout: defaultRequestTimeout, TargetTimeout: defaultResponseTimeout}
	defaultRequestTimeout    = 30 * time.Second
	defaultResponseTimeout   = 5 * time.Second
)

func testTarget(t *testing.T, handler http.HandlerFunc) *Target {
	_, targetURL := testBackendWithHandler(t, handler)

	target, err := NewTarget(targetURL, defaultHealthCheckConfig, defaultRequestTimeout, defaultResponseTimeout)
	require.NoError(t, err)
	return target
}

func testBackend(t *testing.T, body string, statusCode int) (*httptest.Server, string) {
	return testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	})
}

func testBackendWithHandler(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	return server, serverURL.Host
}

func testServer(t *testing.T) (*Server, string) {
	config := &Config{
		Bind:      "127.0.0.1",
		HttpPort:  0,
		HttpsPort: 0,
		ConfigDir: shortTmpDir(t),
	}
	router := NewRouter(config.StatePath())
	server := NewServer(config, router)
	server.Start()

	t.Cleanup(server.Stop)

	addr := fmt.Sprintf("http://localhost:%d", server.HttpPort())

	return server, addr
}

func shortTmpDir(t *testing.T) string {
	path := "/tmp/" + uuid.New().String()
	os.Mkdir(path, 0755)

	t.Cleanup(func() {
		os.RemoveAll(path)
	})

	return path
}
