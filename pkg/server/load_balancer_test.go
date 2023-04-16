package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadBalancer_Empty(t *testing.T) {
	lb := NewLoadBalancer()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	lb.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Result().StatusCode)
}

func TestLoadBalancer_SingleService(t *testing.T) {
	lb := NewLoadBalancer()
	_, backendURL := testBackend(t, "first")

	require.NoError(t, lb.Add([]*url.URL{backendURL}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	lb.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "first", string(w.Body.String()))
}

func TestLoadBalancer_RoundRobinBetweenMultipleServices(t *testing.T) {
	lb := NewLoadBalancer()

	for i := 0; i < 5; i++ {
		_, backendURL := testBackend(t, strconv.Itoa(i))
		lb.Add([]*url.URL{backendURL})
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
	lb := NewLoadBalancer()
	_, backendURL := testBackend(t, "first")

	for i := 0; i < 5; i++ {
		lb.Add([]*url.URL{backendURL})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		lb.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Result().StatusCode)
		require.Equal(t, "first", string(w.Body.String()))

		lb.Remove([]*url.URL{backendURL})
	}

	require.Empty(t, lb.GetServices())
}
