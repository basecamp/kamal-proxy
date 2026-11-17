package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_PathServiceBypassingWildcardClientCA(t *testing.T) {
	router := testRouter(t)
	_, mtls := testBackend(t, "mtls", http.StatusOK)
	_, api := testBackend(t, "api", http.StatusOK)

	certPath, keyPath := prepareTestCertificateFiles(t)
	clientCAOptions := defaultServiceOptions
	clientCAOptions.Hosts = []string{"*.example.com"}
	clientCAOptions.TLSEnabled = true
	clientCAOptions.TLSCertificatePath = certPath
	clientCAOptions.TLSPrivateKeyPath = keyPath
	clientCAOptions.TLSClientCAPath = generateTestCA(t).certPath

	pathOptions := defaultServiceOptions
	pathOptions.Hosts = []string{"app.example.com"}
	pathOptions.PathPrefixes = []string{"/api"}

	t.Run("rejects path service deployed after the wildcard host", func(t *testing.T) {
		require.NoError(t, router.DeployService("mtls", []string{mtls}, defaultEmptyReaders, clientCAOptions, defaultTargetOptions, defaultDeploymentOptions))

		err := router.DeployService("api", []string{api}, defaultEmptyReaders, pathOptions, defaultTargetOptions, defaultDeploymentOptions)
		require.Equal(t, ErrorHostBypassesClientCA, err)

		statusCode, _ := sendGETRequest(router, "http://app.example.com/api")
		assert.Equal(t, http.StatusMovedPermanently, statusCode)

		require.NoError(t, router.RemoveService("mtls"))
	})

	t.Run("rejects wildcard host deployed after the path service", func(t *testing.T) {
		require.NoError(t, router.DeployService("api", []string{api}, defaultEmptyReaders, pathOptions, defaultTargetOptions, defaultDeploymentOptions))

		err := router.DeployService("mtls", []string{mtls}, defaultEmptyReaders, clientCAOptions, defaultTargetOptions, defaultDeploymentOptions)
		require.Equal(t, ErrorHostBypassesClientCA, err)

		require.NoError(t, router.RemoveService("api"))
	})
}

func TestRouter_RemovingRootServiceKeepsWildcardClientCA(t *testing.T) {
	router := testRouter(t)
	_, mtls := testBackend(t, "mtls", http.StatusOK)
	_, root := testBackend(t, "root", http.StatusOK)

	var apiReached atomic.Bool
	_, api := testBackendWithHandler(t, recordRequestsExceptHealthChecks(&apiReached))

	certPath, keyPath := prepareTestCertificateFiles(t)
	tlsOptions := defaultServiceOptions
	tlsOptions.TLSEnabled = true
	tlsOptions.TLSCertificatePath = certPath
	tlsOptions.TLSPrivateKeyPath = keyPath

	clientCAOptions := tlsOptions
	clientCAOptions.Hosts = []string{"*.example.com"}
	clientCAOptions.TLSClientCAPath = generateTestCA(t).certPath

	rootOptions := tlsOptions
	rootOptions.Hosts = []string{"app.example.com"}

	pathOptions := defaultServiceOptions
	pathOptions.Hosts = []string{"app.example.com"}
	pathOptions.PathPrefixes = []string{"/api"}

	require.NoError(t, router.DeployService("mtls", []string{mtls}, defaultEmptyReaders, clientCAOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.DeployService("root", []string{root}, defaultEmptyReaders, rootOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.DeployService("api", []string{api}, defaultEmptyReaders, pathOptions, defaultTargetOptions, defaultDeploymentOptions))

	logs := captureLogs(t)
	require.NoError(t, router.RemoveService("root"))

	assert.Contains(t, logs.String(), "Host has path-based services but no root service")
	assert.Contains(t, logs.String(), "app.example.com")

	statusCode, _ := sendGETRequest(router, "http://app.example.com/api")
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.False(t, apiReached.Load())
}

func TestRouter_RemovingServiceWithoutBypassingClientCADoesNotWarn(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "target", http.StatusOK)
	require.NoError(t, router.DeployService("service", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	logs := captureLogs(t)
	require.NoError(t, router.RemoveService("service"))

	assert.NotContains(t, logs.String(), "Host has path-based services but no root service")
}

func TestRouter_ClientCALookupsDuringRedeploys(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackend(t, "ok", http.StatusOK)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.TLSClientCAPath = generateTestCA(t).certPath

	deploy := func() {
		require.NoError(t, router.DeployService("mtls", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	}
	deploy()

	var redeploying atomic.Bool
	redeploying.Store(true)
	redeployed := make(chan struct{})
	go func() {
		defer close(redeployed)
		defer redeploying.Store(false)
		for range 20 {
			deploy()
		}
	}()

	for redeploying.Load() {
		statusCode, _ := sendGETRequest(router, "http://example.com/")
		assert.Equal(t, http.StatusMovedPermanently, statusCode)
		assert.NotNil(t, router.clientCA("example.com"))
	}
	<-redeployed
}

func TestRouter_PlainHTTPToClientCAHostRedirectsIPAddresses(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackend(t, "ok", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSOnDemandURL = "/check"
	serviceOptions.ACMECachePath = t.TempDir()
	serviceOptions.TLSClientCAPath = generateTestCA(t).certPath

	require.NoError(t, router.DeployService("mtls", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	checkRedirect := func(url, expectedLocation string) {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))

		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, expectedLocation, w.Header().Get("Location"))
	}

	checkRedirect("http://[2001:db8::1]:8080/api?page=2", "https://[2001:db8::1]/api?page=2")
	checkRedirect("http://[2001:db8::1]/api", "https://[2001:db8::1]/api")
	checkRedirect("http://192.0.2.1:8080/api", "https://192.0.2.1/api")
	checkRedirect("http://app.example.com:8080/api", "https://app.example.com/api")
}

// Helpers

type lockedLogBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (l *lockedLogBuffer) Write(p []byte) (int, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.buffer.Write(p)
}

func (l *lockedLogBuffer) String() string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.buffer.String()
}

func captureLogs(t *testing.T) *lockedLogBuffer {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}
