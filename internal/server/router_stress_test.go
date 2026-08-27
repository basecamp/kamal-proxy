package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// While all targets are healthy and return 200, a constant stream of requests
// should see nothing but 200s, even as deployments and rollout changes take
// place.
func TestRouter_GaplessDeployAndRolloutUnderLoad(t *testing.T) {
	router := testRouter(t)
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)
	_, rollout := testBackend(t, "rollout", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{first}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	var requestCount atomic.Int64
	var failures sync.Map
	stop := make(chan struct{})

	newRequest := func(iteration int) *http.Request {
		switch iteration % 3 {
		case 0:
			return httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
		case 1:
			req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			req.AddCookie(&http.Cookie{Name: RolloutCookieName, Value: "stress"})
			return req
		default:
			return httptest.NewRequest(http.MethodPost, "http://example.com/", nil)
		}
	}

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for iteration := 0; ; iteration++ {
				select {
				case <-stop:
					return
				default:
				}

				statusCode, body := sendRequest(router, newRequest(iteration))
				requestCount.Add(1)
				if statusCode != http.StatusOK {
					failures.Store(fmt.Sprintf("%d: %s", statusCode, body), true)
				}
			}
		})
	}

	activeTargets := [][]string{{first}, {second}}
	for i := range 20 {
		require.NoError(t, router.DeployService("service1", activeTargets[i%2], defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
		require.NoError(t, router.SetRolloutTargets("service1", []string{rollout}, defaultEmptyReaders, defaultDeploymentOptions))
		require.NoError(t, router.EnableRollout("service1"))
		require.NoError(t, router.SetRolloutSplit("service1", 50, []string{"stress"}))
		require.NoError(t, router.SetRolloutSplit("service1", 100, []string{"stress"}))
		require.NoError(t, router.DisableRollout("service1"))
		require.NoError(t, router.RemoveRolloutTargets("service1", DefaultDrainTimeout))
	}

	close(stop)
	workers.Wait()

	failures.Range(func(key, _ any) bool {
		t.Errorf("non-200 response during deploy/rollout churn: %s", key)
		return true
	})
	require.Greater(t, requestCount.Load(), int64(100))
}

// Redeploying an existing service with changed options while it is serving
// traffic. Run with -race to check that option updates are synchronized with
// the request path.
func TestRouter_RedeployWhileServingUnderLoad(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackend(t, "ok", http.StatusOK)

	require.NoError(t, router.DeployService("service1", []string{backend}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	for i := range 100 {
		options := defaultServiceOptions
		options.StripPrefix = i%2 == 0

		PerformConcurrently(
			func() {
				statusCode, _ := sendGETRequest(router, "http://example.com/")
				assert.Equal(t, http.StatusOK, statusCode)
			},
			func() {
				assert.NoError(t, router.DeployService("service1", []string{backend}, defaultEmptyReaders, options, defaultTargetOptions, defaultDeploymentOptions))
			},
		)
	}
}

// Redeploying one service while a sibling service, mounted at a path prefix on
// the same host, is serving traffic. Run with -race to check that the TLS
// option sync between services is synchronized with the request path.
func TestRouter_DeploySiblingWhileServingUnderLoad(t *testing.T) {
	router := testRouter(t)
	_, backend := testBackend(t, "ok", http.StatusOK)

	apiOptions := defaultServiceOptions
	apiOptions.PathPrefixes = []string{"/api"}
	require.NoError(t, router.DeployService("api", []string{backend}, defaultEmptyReaders, apiOptions, defaultTargetOptions, defaultDeploymentOptions))
	require.NoError(t, router.DeployService("root", []string{backend}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	for range 100 {
		PerformConcurrently(
			func() {
				statusCode, _ := sendGETRequest(router, "http://example.com/api")
				assert.Equal(t, http.StatusOK, statusCode)
			},
			func() {
				assert.NoError(t, router.DeployService("root", []string{backend}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
			},
		)
	}
}
