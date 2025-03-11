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

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_Removing(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.RemoveService("service1"))
	statusCode, _ = sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForMultipleHosts(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"1.example.com", "2.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, _ = sendGETRequest(router, "http://3.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_UpdatingHostsOfActiveService(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"1.example.com", "2.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	require.NoError(t, router.SetServiceTarget("service1", []string{"3.example.com", "2.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendGETRequest(router, "http://1.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)

	statusCode, body := sendGETRequest(router, "http://2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://3.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ActiveServiceForUnknownHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendGETRequest(router, "http://other.example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForHostContainingPort(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com:80/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ActiveServiceWithoutHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReplacingActiveService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusCode)

	targetOptions.BufferRequests = false
	targetOptions.MaxRequestBodySize = 0
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	serviceOptions.TLSEnabled = true
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.Empty(t, body)
}

func TestRouter_DeploymmentsWithErrorsDoNotUpdateService(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	ensureServiceIsHealthy := func() {
		statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Hello")))
		assert.Equal(t, http.StatusOK, statusCode)
		assert.Equal(t, "first", body)
	}

	require.NoError(t, router.SetServiceTarget("service1", []string{"example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	ensureServiceIsHealthy()

	t.Run("custom TLS that is not valid", func(t *testing.T) {
		serviceOptions := ServiceOptions{TLSEnabled: true, TLSCertificatePath: "not valid", TLSPrivateKeyPath: "not valid"}
		require.Error(t, router.SetServiceTarget("service1", []string{"example.com"}, defaultPaths, target, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

		ensureServiceIsHealthy()
	})

	t.Run("custom error pages that are not valid", func(t *testing.T) {
		serviceOptions := ServiceOptions{ErrorPagePath: "not valid"}
		require.Error(t, router.SetServiceTarget("service1", []string{"example.com"}, defaultPaths, target, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

		ensureServiceIsHealthy()
	})
}

func TestRouter_UpdatingPauseStateIndependentlyOfDeployments(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	router.PauseService("service1", time.Second, time.Millisecond*10)

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	router.ResumeService("service1")

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
}

func TestRouter_ChangingHostForService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy2.example.com"}, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	err := router.SetServiceTarget("service12", []string{"dummy.example.com"}, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)

	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReusingEmptyHost(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	err := router.SetServiceTarget("service12", defaultEmptyHosts, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)

	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://anything.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_RoutingMultipleHosts(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"s1.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", []string{"s2.example.com"}, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://s1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://s2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_PathBasedRoutingStripPrefix(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.String()))
	})

	serviceOptions := defaultServiceOptions
	serviceOptions.StripPrefix = true

	require.NoError(t, router.SetServiceTarget("service1", []string{"example.com"}, defaultPaths, backend, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", []string{"example.com"}, []string{"/app"}, backend, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service3", []string{"example.com"}, []string{"/api/internal"}, backend, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://example.com/app/show")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/show", body)

	statusCode, body = sendGETRequest(router, "http://example.com/app")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/", body)

	statusCode, body = sendGETRequest(router, "http://example.com/api/internal/something?a=b")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/something?a=b", body)

	statusCode, body = sendGETRequest(router, "http://example.com/api/external/something")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/api/external/something", body)

	statusCode, body = sendGETRequest(router, "http://example.com/appointment")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/appointment", body)

	serviceOptions.StripPrefix = false

	require.NoError(t, router.SetServiceTarget("service2", []string{"example.com"}, []string{"/app"}, backend, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendGETRequest(router, "http://example.com/app")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/app", body)
}

func TestRouter_PathBasedRoutingWithHosts(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"example.com"}, []string{"/first"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", []string{"example.com"}, []string{"/second"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://example.com/first")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://example.com/second")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, body = sendGETRequest(router, "http://example.com/third")
	assert.Equal(t, http.StatusNotFound, statusCode)

	statusCode, body = sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_PathBasedRoutingWithDefaultHost(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)
	_, third := testBackend(t, "third", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, []string{"/first"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", defaultEmptyHosts, []string{"/second"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service3", []string{"third.example.com"}, []string{"/second"}, third, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://example.com/first")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://example.com/second")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, body = sendGETRequest(router, "http://third.example.com/second/path")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "third", body)

	statusCode, body = sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_TargetWithoutHostActsAsWildcard(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"s1.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("default", defaultEmptyHosts, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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

func TestRouter_TargetsAllowWildcardSubdomains(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)
	_, fallback := testBackend(t, "fallback", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("first", []string{"*.first.example.com"}, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("second", []string{"*.second.example.com"}, defaultPaths, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("fallback", defaultEmptyHosts, defaultPaths, fallback, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://app.first.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://api.second.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, body = sendGETRequest(router, "http://something-else.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "fallback", body)
}

func TestRouter_WildcardDomainsCannotBeUsedWithAutomaticTLS(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)

	err := router.SetServiceTarget("first", []string{"first.example.com", "*.first.example.com"}, defaultPaths, first, ServiceOptions{TLSEnabled: true}, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)
	require.Equal(t, ErrorAutomaticTLSDoesNotSupportWildcards, err)
}

func TestRouter_ServiceFailingToBecomeHealthy(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "", http.StatusInternalServerError)

	err := router.SetServiceTarget("example", []string{"example.com"}, defaultPaths, target, defaultServiceOptions, defaultTargetOptions, time.Millisecond*20, DefaultDrainTimeout)
	assert.ErrorIs(t, err, ErrorTargetFailedToBecomeHealthy)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_EnablingRollout(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
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
	_, third := testBackend(t, "third", http.StatusOK)

	router := NewRouter(statePath)
	require.NoError(t, router.SetServiceTarget("default", defaultEmptyHosts, defaultPaths, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("other1", []string{"other.example.com"}, defaultPaths, second, ServiceOptions{TLSEnabled: true}, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("other2", []string{"other.example.com"}, []string{"/api"}, third, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://something.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, _ = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusMovedPermanently, statusCode)

	statusCode, body = sendGETRequest(router, "https://other.example.com/api")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "third", body)

	router = NewRouter(statePath)
	router.RestoreLastSavedState()

	statusCode, body = sendGETRequest(router, "http://something.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, _ = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusMovedPermanently, statusCode)

	statusCode, body = sendGETRequest(router, "https://other.example.com/api")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "third", body)
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
