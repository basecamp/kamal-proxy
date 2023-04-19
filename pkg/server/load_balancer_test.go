package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var defaultHealthCheckConfig = HealthCheckConfig{
	HealthCheckPath:     DefaultHealthCheckPath,
	HealthCheckInterval: DefaultHealthCheckInterval,
	HealthCheckTimeout:  DefaultHealthCheckTimeout,
}

var typicalConfig = Config{
	AddTimeout:        time.Second * 5,
	DrainTimeout:      time.Second * 5,
	ConfigDir:         os.TempDir(),
	HealthCheckConfig: defaultHealthCheckConfig,
}

func TestLoadBalancer_Empty(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	lb.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Result().StatusCode)
}

func TestLoadBalancer_SingleService(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, backendURL := testBackend(t, "first")

	require.NoError(t, lb.Add([]*url.URL{backendURL}, true))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	lb.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "first", string(w.Body.String()))
}

func TestLoadBalancer_RoundRobinBetweenMultipleServices(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)

	for i := 0; i < 5; i++ {
		_, backendURL := testBackend(t, strconv.Itoa(i))
		lb.Add([]*url.URL{backendURL}, true)
	}

	results := []string{}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		lb.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		results = append(results, w.Body.String())
	}

	sort.Strings(results)
	require.Equal(t, []string{"0", "1", "2", "3", "4"}, results)

}

func TestLoadBalancer_AddAndRemoveSameService(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, backendURL := testBackend(t, "first")

	for i := 0; i < 5; i++ {
		lb.Add([]*url.URL{backendURL}, true)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		lb.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		require.Equal(t, "first", string(w.Body.String()))

		lb.Remove([]*url.URL{backendURL})
	}

	require.Empty(t, lb.GetServices())
}

func TestLoadBalancer_RestoreStateOnRestart(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, backendURL := testBackend(t, "first")

	lb.Add([]*url.URL{backendURL}, true)
	services := lb.GetServices()

	require.Equal(t, 1, len(services))
	require.Equal(t, ServiceStateHealthy, services[0].state)

	lb2 := NewLoadBalancer(typicalConfig)
	require.NoError(t, lb2.RestoreFromStateFile())

	services2 := lb2.GetServices()
	require.Equal(t, 1, len(services2))
	services2[0].WaitUntilHealthy(time.Second)

	require.Equal(t, ServiceStateHealthy, services2[0].state)
	require.Equal(t, services[0].Host(), services2[0].Host())
}

func TestLoadBalancer_RestoreEmptyStateOnRestart(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, backendURL := testBackend(t, "first")

	lb.Add([]*url.URL{backendURL}, true)
	lb.Remove([]*url.URL{backendURL})

	require.Empty(t, lb.GetServices())

	lb2 := NewLoadBalancer(typicalConfig)
	require.NoError(t, lb2.RestoreFromStateFile())
	require.Empty(t, lb2.GetServices())
}

func TestLoadBalancer_DeployNewSetOfServices(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, backend1URL := testBackend(t, "first")
	_, backend2URL := testBackend(t, "first")
	_, backend3URL := testBackend(t, "first")

	isDeployed := func(hostURL *url.URL) bool {
		services := lb.GetServices()
		for _, s := range services {
			if s.Host() == hostURL.Host {
				return true
			}
		}
		return false
	}

	lb.Deploy(HostURLs{backend2URL, backend3URL})

	require.Len(t, lb.GetServices(), 2)
	require.True(t, isDeployed(backend2URL))
	require.True(t, isDeployed(backend3URL))

	lb.Deploy(HostURLs{backend1URL, backend3URL})

	require.Len(t, lb.GetServices(), 2)
	require.True(t, isDeployed(backend1URL))
	require.True(t, isDeployed(backend3URL))

	lb.Deploy(HostURLs{backend2URL})

	require.Len(t, lb.GetServices(), 1)
	require.True(t, isDeployed(backend2URL))
}
