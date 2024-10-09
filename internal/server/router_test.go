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
	t.Parallel()

	router := testRouter(t)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForHost(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_Removing(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.RemoveService("service1"))
	statusCode, _ = sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForMultipleHosts(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"1.example.com", "2.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"1.example.com", "2.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	require.NoError(t, router.SetServiceTarget("service1", []string{"3.example.com", "2.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendGETRequest(router, "http://other.example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ActiveServiceForHostContainingPort(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com:80/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ActiveServiceWithoutHost(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReplacingActiveService(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_UpdatingOptions(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	targetOptions := defaultTargetOptions

	targetOptions.BufferRequests = true
	targetOptions.MaxRequestBodySize = 10
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusCode)

	targetOptions.BufferRequests = false
	targetOptions.MaxRequestBodySize = 0
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	serviceOptions.TLSEnabled = true
	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, serviceOptions, targetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.Empty(t, body)
}

func TestRouter_DeploymmentsWithErrorsDoNotUpdateService(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	ensureServiceIsHealthy := func() {
		statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com", strings.NewReader("Hello")))
		assert.Equal(t, http.StatusOK, statusCode)
		assert.Equal(t, "first", body)
	}

	require.NoError(t, router.SetServiceTarget("service1", []string{"example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	ensureServiceIsHealthy()

	t.Run("custom TLS that is not valid", func(t *testing.T) {
		t.Parallel()

		serviceOptions := ServiceOptions{TLSEnabled: true, TLSCertificatePath: "not valid", TLSPrivateKeyPath: "not valid"}
		require.Error(t, router.SetServiceTarget("service1", []string{"example.com"}, target, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

		ensureServiceIsHealthy()
	})

	t.Run("custom error pages that are not valid", func(t *testing.T) {
		t.Parallel()

		serviceOptions := ServiceOptions{ErrorPagePath: "not valid"}
		require.Error(t, router.SetServiceTarget("service1", []string{"example.com"}, target, serviceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

		ensureServiceIsHealthy()
	})
}

func TestRouter_UpdatingPauseStateIndependentlyOfDeployments(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	router.PauseService("service1", time.Second, time.Millisecond*10)

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, target, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	router.ResumeService("service1")

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
}

func TestRouter_ChangingHostForService(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy2.example.com"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body = sendGETRequest(router, "http://dummy2.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, _ = sendGETRequest(router, "http://dummy.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_ReusingHost(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"dummy.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	err := router.SetServiceTarget("service12", []string{"dummy.example.com"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)

	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReusingEmptyHost(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	err := router.SetServiceTarget("service12", defaultEmptyHosts, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)

	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://anything.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_RoutingMultipleHosts(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"s1.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("service2", []string{"s2.example.com"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://s1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://s2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_TargetWithoutHostActsAsWildcard(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", []string{"s1.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("default", defaultEmptyHosts, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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

	require.NoError(t, router.SetServiceTarget("first", []string{"*.first.example.com"}, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("second", []string{"*.second.example.com"}, second, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("fallback", defaultEmptyHosts, fallback, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

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

	err := router.SetServiceTarget("first", []string{"first.example.com", "*.first.example.com"}, first, ServiceOptions{TLSEnabled: true}, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout)
	require.Equal(t, ErrorAutomaticTLSDoesNotSupportWildcards, err)
}

func TestRouter_ServiceFailingToBecomeHealthy(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, target := testBackend(t, "", http.StatusInternalServerError)

	err := router.SetServiceTarget("example", []string{"example.com"}, target, defaultServiceOptions, defaultTargetOptions, time.Millisecond*20, DefaultDrainTimeout)
	assert.Equal(t, ErrorTargetFailedToBecomeHealthy, err)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_EnablingRollout(t *testing.T) {
	t.Parallel()

	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.SetServiceTarget("service1", defaultEmptyHosts, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
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
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "state.json")

	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	router := NewRouter(statePath)
	require.NoError(t, router.SetServiceTarget("default", defaultEmptyHosts, first, defaultServiceOptions, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))
	require.NoError(t, router.SetServiceTarget("other", []string{"other.example.com"}, second, ServiceOptions{TLSEnabled: true}, defaultTargetOptions, DefaultDeployTimeout, DefaultDrainTimeout))

	statusCode, body := sendGETRequest(router, "http://something.example.com")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, _ = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusMovedPermanently, statusCode)

	router = NewRouter(statePath)
	router.RestoreLastSavedState()

	statusCode, body = sendGETRequest(router, "http://something.example.com")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, _ = sendGETRequest(router, "http://other.example.com/")
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
}

func TestHostServiceMap_ServiceForHost(t *testing.T) {
	hsm := HostServiceMap{
		"example.com":     &Service{name: "1"},
		"app.example.com": &Service{name: "2"},
		"api.example.com": &Service{name: "3"},
		"*.example.com":   &Service{name: "4"},
		"":                &Service{name: "5"},
	}

	assert.Equal(t, "1", hsm.ServiceForHost("example.com").name)
	assert.Equal(t, "2", hsm.ServiceForHost("app.example.com").name)
	assert.Equal(t, "3", hsm.ServiceForHost("api.example.com").name)
	assert.Equal(t, "4", hsm.ServiceForHost("anything.example.com").name)

	assert.Equal(t, "5", hsm.ServiceForHost("extra.level.example.com").name)
	assert.Equal(t, "5", hsm.ServiceForHost("other.com").name)

	hsm = HostServiceMap{
		"example.com": &Service{name: "1"},
	}

	assert.Nil(t, hsm.ServiceForHost("app.example.com"))
}

func BenchmarkHostServiceMap_WilcardRouting(b *testing.B) {
	hsm := HostServiceMap{
		"one.example.com":   &Service{},
		"*.two.example.com": &Service{},
		"":                  &Service{},
	}

	b.Run("exact match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("one.example.com")
		}
	})

	b.Run("wildcard match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("anything.two.example.com")
		}
	})

	b.Run("default match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("missing.example.com")
		}
	})
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
