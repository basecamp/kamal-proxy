package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_Empty(t *testing.T) {
	router := testRouter(t)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ActiveServiceForUnknownHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendGETRequest(router, "http://other.example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForHostContainingPort(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com:80/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ActiveServiceWithoutHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReplacingActiveService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_UpdatingOptions(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	targetOptions := defaultTargetOptions

	targetOptions.BufferRequests = true
	targetOptions.MaxRequestBodySize = 10
	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusCode)

	targetOptions.BufferRequests = false
	targetOptions.MaxRequestBodySize = 0
	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	serviceOptions.TLSHostname = "dummy.example.com"
	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.Empty(t, body)
}

func TestRouter_UpdatingPauseStateIndependentlyOfDeployments(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	router.PauseService("service1", time.Second, time.Millisecond*10)

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	router.ResumeService("service1")

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
}

func TestRouter_ChangingHostForService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", "dummy2.example.com", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendGETRequest(router, "http://dummy2.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, _ = sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ReusingHost(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "dummy.example.com", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	err := router.SetServiceTarget("service12", "dummy.example.com", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)

	require.EqualError(t, err, "host is used by another service", "Error message does not match expected one")

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_RoutingMultipleHosts(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "s1.example.com", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", "s2.example.com", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://s1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://s2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_TargetWithoutHostActsAsWildcard(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "s1.example.com", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("default", "", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://s1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://s2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, body = sendGETRequest(router, "http://s3.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_ServiceFailingToBecomeHealthy(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "", http.StatusInternalServerError)

	err := router.SetServiceTarget("example", "example.com", target, defaultServiceOptions, defaultTargetOptions, time.Millisecond*20, DefaultDrainTimeout)
	assert.Equal(t, ErrorTargetFailedToBecomeHealthy, err)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_EnablingRollout(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", "", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetRolloutTarget("service1", second, DefaultDeployTimeout, DefaultDrainTimeout))

	checkResponse := func(expected string) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		req.AddCookie(&http.Cookie{Name: "kamal-rollout", Value: "1"})
		statusCode, body := sendRequest(router, req)
		assert.Equal(t, http.StatusOK, statusCode)
		assert.Equal(t, expected, body)
	}

	checkResponse("first")

	require.NoError(t, router.SetRolloutSplit("service1", 0, []string{"1"}))
	checkResponse("second")

	require.NoError(t, router.SetRolloutSplit("service1", 0, []string{"2"}))
	checkResponse("first")

	require.NoError(t, router.StopRollout("service1"))
	checkResponse("first")
}

func TestRouter_RestoreLastSavedState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")

	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	router := NewRouter(statePath)
	require.NoError(t, router.SetServiceTarget("default", "", first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("other", "other.example.com", second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://something.example.com")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	router = NewRouter(statePath)
	router.RestoreLastSavedState()

	statusCode, body = sendGETRequest(router, "http://something.example.com")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

// Helpers

func testRouter(t *testing.T) *Router {
	statePath := filepath.Join(t.TempDir(), "state.json")
	return NewRouter(statePath)
}

func sendGETRequest(router *Router, url string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	return sendRequest(router, req)
}

func sendRequest(router *Router, req *http.Request) (int, string) {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Result().StatusCode, string(w.Body.String())
}
