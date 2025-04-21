package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetList_NewTargetListIllegalNames(t *testing.T) {
	_, err := NewTargetList([]string{"", "_", "/"}, TargetOptions{})
	assert.Error(t, err, ErrorInvalidHostPattern)
}

func TestTargetList_Names(t *testing.T) {
	tl, err := NewTargetList([]string{"one", "two", "three"}, TargetOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"one", "two", "three"}, tl.Names())
}

func TestLoadBalancer_Targets(t *testing.T) {
	tl, err := NewTargetList([]string{"one", "two", "three"}, defaultTargetOptions)
	require.NoError(t, err)

	lb := NewLoadBalancer(tl)
	defer lb.Dispose()

	assert.Equal(t, []string{"one", "two", "three"}, lb.Targets().Names())
}

func TestLoadBalancer_WaitUntilTargetHealthy(t *testing.T) {
	lb := testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	unhealthyTarget := lb.Targets()[0]
	err := lb.WaitUntilTargetHealthy(unhealthyTarget, time.Millisecond*100)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrorTargetFailedToUpdateLBStatus)

	healthyTarget := lb.Targets()[1]
	time.Sleep(100 * time.Millisecond)
	err = lb.WaitUntilTargetHealthy(healthyTarget, time.Second)
	require.NoError(t, err)

	lb = testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) },
	)

	time.Sleep(100 * time.Millisecond)

	for _, target := range lb.Targets() {
		err = lb.WaitUntilTargetHealthy(target, time.Second)
		require.NoError(t, err)
	}
}

func TestLoadBalancer_ServeHTTP(t *testing.T) {
	lb := testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("one"))
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("two"))
		},
	)

	time.Sleep(100 * time.Millisecond)
	for _, target := range lb.Targets() {
		require.NoError(t, lb.WaitUntilTargetHealthy(target, time.Second))
	}

	bodies := []string{}
	for range 4 {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		lb.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		bodies = append(bodies, w.Body.String())
	}

	assert.Contains(t, bodies, "one")
	assert.Contains(t, bodies, "two")
}

// Helpers

func testLoadBalancerWithHandlers(t *testing.T, handlers ...http.HandlerFunc) *LoadBalancer {
	targets := []string{}
	for _, h := range handlers {
		targets = append(targets, testTarget(t, h).Target())
	}

	tl, err := NewTargetList(targets, defaultTargetOptions)
	require.NoError(t, err)

	lb := NewLoadBalancer(tl)
	t.Cleanup(lb.Dispose)

	return lb
}
