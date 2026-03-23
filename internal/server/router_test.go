package server

import (
	"encoding/json"
    "crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestRouter_DeployService(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_DeployServiceMultipleTargets(t *testing.T) {
	router := testRouter(t)
	_, firstTarget := testBackend(t, "first", http.StatusOK)
	_, secondTarget := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{firstTarget, secondTarget}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	bodies := []string{}
	for range 4 {
		statusCode, body := sendGETRequest(router, "http://example.com/")
		assert.Equal(t, http.StatusOK, statusCode)
		bodies = append(bodies, body)
	}

	assert.Contains(t, bodies, "first")
	assert.Contains(t, bodies, "second")
}

func TestRouter_Removing(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.RemoveService("service1"))
	statusCode, _ = sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_DeployServiceMultipleHosts(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"1.example.com", "2.example.com"}

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

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

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"1.example.com", "2.example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	serviceOptions.Hosts = []string{"3.example.com", "2.example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, _ := sendGETRequest(router, "http://1.example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)

	statusCode, body := sendGETRequest(router, "http://2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://3.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_DeployServiceUnknownHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"dummy.example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, _ := sendGETRequest(router, "http://other.example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_DeployServiceContainingPort(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"dummy.example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com:80/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_DeployServiceWithoutHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReplacingActiveService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	require.NoError(t, router.DeployService("service1", []string{second}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body = sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_UpdatingOptions(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"dummy.example.com"}

	targetOptions := defaultTargetOptions
	targetOptions.BufferRequests = true
	targetOptions.MaxRequestBodySize = 10

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusRequestEntityTooLarge, statusCode)

	targetOptions.BufferRequests = false
	targetOptions.MaxRequestBodySize = 0
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

	statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	serviceOptions.TLSEnabled = true
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

	statusCode, body = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://dummy.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.Empty(t, body)

	serviceOptions.Hosts = []string{"other.example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

	statusCode, body = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://other.example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusMovedPermanently, statusCode)
	assert.Empty(t, body)
}

func TestRouter_CanonicalHostRedirect(t *testing.T) {
    router := testRouter(t)
    _, target := testBackend(t, "first", http.StatusOK)

    serviceOptions := defaultServiceOptions
    serviceOptions.Hosts = []string{"example.com", "www.example.com"}
    serviceOptions.CanonicalHost = "example.com"

    require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

    statusCode, _ := sendGETRequest(router, "http://www.example.com/")
    assert.Equal(t, http.StatusMovedPermanently, statusCode)

    statusCode, body := sendGETRequest(router, "http://example.com/")
    assert.Equal(t, http.StatusOK, statusCode)
    assert.Equal(t, "first", body)
}

func TestRouter_CanonicalHostRedirectWithTLS(t *testing.T) {
    router := testRouter(t)
    _, target := testBackend(t, "first", http.StatusOK)

    serviceOptions := defaultServiceOptions
    serviceOptions.Hosts = []string{"example.com", "www.example.com"}
    serviceOptions.CanonicalHost = "example.com"
    serviceOptions.TLSEnabled = true
    serviceOptions.TLSRedirect = true

    require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

    // Should go directly to https://example.com in a single redirect
    statusCode, _ := sendGETRequest(router, "http://www.example.com/")
    assert.Equal(t, http.StatusMovedPermanently, statusCode)

    // HTTPS request to non-canonical host should redirect to canonical host but remain HTTPS
    req := httptest.NewRequest(http.MethodGet, "https://www.example.com/", nil)
    req.TLS = &tls.ConnectionState{}
    w := httptest.NewRecorder()
    router.ServeHTTP(w, req)
    assert.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)
}

func TestRouter_DeploymentsWithErrorsDoNotUpdateService(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	ensureServiceIsHealthy := func() {
		statusCode, body := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Hello")))
		assert.Equal(t, http.StatusOK, statusCode)
		assert.Equal(t, "first", body)
	}

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}

	targetOptions := defaultTargetOptions

	assert.NoFileExists(t, router.statePath)
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))
	ensureServiceIsHealthy()
	require.FileExists(t, router.statePath)

	ensureStateWasNotSaved := func() {
		f, err := os.OpenFile(router.statePath, os.O_RDONLY, 0600)
		require.NoError(t, err)

		var services []*Service
		require.NoError(t, json.NewDecoder(f).Decode(&services), "if this test failed it means an invalid config prevents the system from booting")

		sm := NewServiceMap()
		for _, service := range services {
			sm.Set(service)
		}
		persistedOptions := sm.Get("service1").options
		persistedTargetOptions := sm.Get("service1").targetOptions

		assert.Equal(t, serviceOptions.TLSPrivateKeyPath, persistedOptions.TLSPrivateKeyPath)
		assert.Equal(t, serviceOptions.TLSCertificatePath, persistedOptions.TLSCertificatePath)
		assert.Equal(t, serviceOptions.TLSEnabled, persistedOptions.TLSEnabled)
		assert.Equal(t, serviceOptions.ErrorPagePath, persistedOptions.ErrorPagePath)
		assert.Equal(t, targetOptions.BufferRequests, persistedTargetOptions.BufferRequests)
	}

	t.Run("custom TLS that is not valid", func(t *testing.T) {
		newServiceOptions := ServiceOptions{TLSEnabled: true, TLSCertificatePath: "not valid", TLSPrivateKeyPath: "not valid"}
		newTargetOptions := TargetOptions{BufferRequests: true, HealthCheckConfig: defaultHealthCheckConfig}

		require.Error(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, newServiceOptions, newTargetOptions, defaultDeploymentOptions))

		ensureServiceIsHealthy()
		ensureStateWasNotSaved()
	})

	t.Run("custom error pages that are not valid", func(t *testing.T) {
		newServiceOptions := ServiceOptions{ErrorPagePath: "not valid"}
		newTargetOptions := TargetOptions{BufferRequests: true, HealthCheckConfig: defaultHealthCheckConfig}

		require.Error(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, newServiceOptions, newTargetOptions, defaultDeploymentOptions))

		ensureServiceIsHealthy()
		ensureStateWasNotSaved()
	})
}

func TestRouter_UpdatingPauseStateIndependentlyOfDeployments(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
	router.PauseService("service1", time.Second, time.Millisecond*10)

	statusCode, _ := sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusGatewayTimeout, statusCode)

	router.ResumeService("service1")

	statusCode, _ = sendRequest(router, httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Something longer than 10")))
	assert.Equal(t, http.StatusOK, statusCode)
}

func TestRouter_ChangingHostForService(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"dummy.example.com"}
	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://dummy.example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	serviceOptions.Hosts = []string{"dummy2.example.com"}
	require.NoError(t, router.DeployService("service1", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

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

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}

	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	err := router.DeployService("service2", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions)
	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_ReusingEmptyHost(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
	err := router.DeployService("service12", []string{second}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions)

	require.Equal(t, ErrorHostInUse, err)

	statusCode, body := sendGETRequest(router, "http://anything.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)
}

func TestRouter_RoutingMultipleHosts(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"s1.example.com"}
	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.Hosts = []string{"s2.example.com"}
	require.NoError(t, router.DeployService("service2", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://s1.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://s2.example.com/")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)
}

func TestRouter_PathBasedRoutingCookiePrefixPrefix(t *testing.T) {
	checkRequest := func(scopeCookiePaths bool, path string, expectedCookiePath string) {
		router := testRouter(t)
		_, backend1 := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    "secret",
				Path:     "/something",
				HttpOnly: true,
			})
			w.WriteHeader(http.StatusOK)
		})
		_, backend2 := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    "secret",
				Path:     "/",
				HttpOnly: true,
			})
			w.WriteHeader(http.StatusOK)
		})

		serviceOptions := defaultServiceOptions
		serviceOptions.StripPrefix = true
		targetOptions := defaultTargetOptions
		targetOptions.ScopeCookiePaths = scopeCookiePaths

		serviceOptions.PathPrefixes = []string{"/api", "/app"}
		require.NoError(t, router.DeployService("service1", []string{backend1}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))
		serviceOptions.PathPrefixes = []string{"/chat"}
		require.NoError(t, router.DeployService("service2", []string{backend2}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		require.Len(t, w.Result().Cookies(), 1)

		cookie := w.Result().Cookies()[0]
		assert.Equal(t, expectedCookiePath, cookie.Path)
	}

	checkRequest(true, "/app", "/app/something")
	checkRequest(true, "/api", "/api/something")
	checkRequest(false, "/app", "/something")
	checkRequest(false, "/api", "/something")

	checkRequest(true, "/chat", "/chat")
	checkRequest(false, "/chat", "/")
}

func TestRouter_PathBasedRoutingCookiePrefixThirdPartyDomain(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:   "first_party",
			Value:  "value1",
			Path:   "/original",
			Domain: "example.com",
		})
		http.SetCookie(w, &http.Cookie{
			Name:   "third_party",
			Value:  "value2",
			Path:   "/original",
			Domain: "other.com",
		})
		http.SetCookie(w, &http.Cookie{
			Name:  "no_domain",
			Value: "value3",
			Path:  "/original",
		})
		w.WriteHeader(http.StatusOK)
	})

	serviceOptions := defaultServiceOptions
	serviceOptions.StripPrefix = true
	serviceOptions.PathPrefixes = []string{"/api"}
	targetOptions := defaultTargetOptions
	targetOptions.ScopeCookiePaths = true

	require.NoError(t, router.DeployService("service", []string{backend}, defaultEmptyReaders, serviceOptions, targetOptions, defaultDeploymentOptions))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Len(t, w.Result().Cookies(), 3)

	cookies := w.Result().Cookies()

	// First-party cookie (domain matches request host) should be scoped
	assert.Equal(t, "first_party", cookies[0].Name)
	assert.Equal(t, "/api/original", cookies[0].Path)

	// Third-party cookie (domain doesn't match) should NOT be scoped
	assert.Equal(t, "third_party", cookies[1].Name)
	assert.Equal(t, "/original", cookies[1].Path)

	// Cookie without domain should be scoped
	assert.Equal(t, "no_domain", cookies[2].Name)
	assert.Equal(t, "/api/original", cookies[2].Path)
}

func TestRouter_PathBasedRoutingStripPrefix(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.String()))
	})

	serviceOptions := defaultServiceOptions
	serviceOptions.StripPrefix = true
	serviceOptions.Hosts = []string{"example.com"}

	require.NoError(t, router.DeployService("service1", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.PathPrefixes = []string{"/app"}
	require.NoError(t, router.DeployService("service2", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.PathPrefixes = []string{"/api/internal"}
	require.NoError(t, router.DeployService("service3", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

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
	serviceOptions.PathPrefixes = []string{"/app"}
	require.NoError(t, router.DeployService("service2", []string{backend}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body = sendGETRequest(router, "http://example.com/app")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "/app", body)
}

func TestRouter_PathBasedRoutingWithHosts(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}

	serviceOptions.PathPrefixes = []string{"/first"}
	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.PathPrefixes = []string{"/second"}
	require.NoError(t, router.DeployService("service2", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://example.com/first")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://example.com/second")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, _ = sendGETRequest(router, "http://example.com/third")
	assert.Equal(t, http.StatusNotFound, statusCode)

	statusCode, _ = sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_PathBasedRoutingWithDefaultHost(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)
	_, third := testBackend(t, "third", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.PathPrefixes = []string{"/first"}
	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.PathPrefixes = []string{"/second"}
	require.NoError(t, router.DeployService("service2", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.Hosts = []string{"third.example.com"}
	serviceOptions.PathPrefixes = []string{"/second"}
	require.NoError(t, router.DeployService("service3", []string{third}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	statusCode, body := sendGETRequest(router, "http://example.com/first")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "first", body)

	statusCode, body = sendGETRequest(router, "http://example.com/second")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "second", body)

	statusCode, body = sendGETRequest(router, "http://third.example.com/second/path")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Equal(t, "third", body)

	statusCode, _ = sendGETRequest(router, "http://example.com/")
	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_TargetWithoutHostActsAsWildcard(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"s1.example.com"}
	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.DeployService("default", []string{second}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

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

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"*.first.example.com"}
	require.NoError(t, router.DeployService("first", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.Hosts = []string{"*.second.example.com"}
	require.NoError(t, router.DeployService("second", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.DeployService("fallback", []string{fallback}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

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

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"first.example.com", "*.first.example.com"}
	serviceOptions.TLSEnabled = true

	err := router.DeployService("first", []string{first}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions)
	require.Equal(t, ErrorAutomaticTLSDoesNotSupportWildcards, err)
}

func TestRouter_ServiceFailingToBecomeHealthy(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "", http.StatusInternalServerError)

	deploymentOptions := defaultDeploymentOptions
	deploymentOptions.DeployTimeout = time.Millisecond * 20
	err := router.DeployService("example", []string{target}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, deploymentOptions)
	assert.ErrorIs(t, err, ErrorTargetFailedToBecomeHealthy)

	statusCode, _ := sendGETRequest(router, "http://example.com/")

	assert.Equal(t, http.StatusNotFound, statusCode)
}

func TestRouter_EnablingRollout(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.SetRolloutTargets("service1", []string{second}, defaultEmptyReaders, defaultDeploymentOptions))

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
	require.NoError(t, router.DeployService("default", []string{first}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"other.example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSRedirect = true
	require.NoError(t, router.DeployService("other1", []string{second}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))
	serviceOptions.PathPrefixes = []string{"/api"}
	serviceOptions.TLSEnabled = false
	serviceOptions.TLSRedirect = false
	require.NoError(t, router.DeployService("other2", []string{third}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

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

func TestRouter_StateFileSurvivesRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	_, target := testBackend(t, "first", http.StatusOK)

	router := NewRouter(statePath)
	require.NoError(t, router.DeployService("service1", []string{target},
		defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	// Verify state file exists and is valid JSON
	f, err := os.Open(statePath)
	require.NoError(t, err)
	defer f.Close()

	var services []*Service
	require.NoError(t, json.NewDecoder(f).Decode(&services))
	assert.Len(t, services, 1)

	// Verify no temp files left behind
	entries, err := os.ReadDir(filepath.Dir(statePath))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".kamal-proxy.state."),
			"temp file should not remain: %s", entry.Name())
	}
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
