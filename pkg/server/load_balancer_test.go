package server

import (
	"net/http"
	"net/http/httptest"
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
	_, host := testBackend(t, "first")

	require.NoError(t, lb.Add(Hosts{host}, true))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	lb.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "first", string(w.Body.String()))
}

func TestLoadBalancer_RoundRobinBetweenMultipleServices(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)

	for i := 0; i < 5; i++ {
		_, host := testBackend(t, strconv.Itoa(i))
		lb.Add(Hosts{host}, true)
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
	_, host := testBackend(t, "first")

	for i := 0; i < 5; i++ {
		lb.Add(Hosts{host}, true)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		lb.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		require.Equal(t, "first", string(w.Body.String()))

		lb.Remove(Hosts{host})
	}

	require.Empty(t, lb.GetServices())
}

func TestLoadBalancer_RestoreStateOnRestart(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, host := testBackend(t, "first")

	lb.Add(Hosts{host}, true)
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
	_, host := testBackend(t, "first")

	lb.Add(Hosts{host}, true)
	lb.Remove(Hosts{host})

	require.Empty(t, lb.GetServices())

	lb2 := NewLoadBalancer(typicalConfig)
	require.NoError(t, lb2.RestoreFromStateFile())
	require.Empty(t, lb2.GetServices())
}

func TestLoadBalancer_DeployNewSetOfServices(t *testing.T) {
	lb := NewLoadBalancer(typicalConfig)
	_, host1 := testBackend(t, "first")
	_, host2 := testBackend(t, "first")
	_, host3 := testBackend(t, "first")

	isDeployed := func(host Host) bool {
		services := lb.GetServices()
		for _, s := range services {
			if s.Host() == host.String() {
				return true
			}
		}
		return false
	}

	lb.Deploy(Hosts{host1, host2})

	require.Len(t, lb.GetServices(), 2)
	require.True(t, isDeployed(host1))
	require.True(t, isDeployed(host2))

	lb.Deploy(Hosts{host1, host3})

	require.Len(t, lb.GetServices(), 2)
	require.True(t, isDeployed(host1))
	require.True(t, isDeployed(host3))

	lb.Deploy(Hosts{host2})

	require.Len(t, lb.GetServices(), 1)
	require.True(t, isDeployed(host2))
}
