package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestService_Serve(t *testing.T) {
	_, host := testBackend(t, "ok")
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
}

func TestService_AddedServiceBecomesHealthy(t *testing.T) {
	_, host := testBackend(t, "ok")
	hostURL, _ := host.ToURL()
	c := &testServiceStateChangeConsumer{}

	s := NewService(hostURL, defaultHealthCheckConfig)
	s.BeginHealthChecks(c)

	require.True(t, s.WaitUntilHealthy(time.Second))
	require.Equal(t, ServiceStateHealthy, s.state)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
	require.True(t, c.called)
}

func TestService_DrainWhenEmpty(t *testing.T) {
	_, host := testBackend(t, "ok")
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)
	s.Drain(time.Second)
}

func TestService_DrainRequestsThatCompleteWithinTimeout(t *testing.T) {
	n := 3
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	_, host := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		time.Sleep(time.Millisecond * 200)
		served++
	})
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go s.ServeHTTP(w, req)
	}

	started.Wait()
	s.Drain(time.Second)

	require.Equal(t, n, served)
}

func TestService_DrainRequestsThatNeedToBeCancelled(t *testing.T) {
	n := 20
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	_, host := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		for i := 0; i < 500; i++ {
			time.Sleep(time.Millisecond * 10)
			if r.Context().Err() != nil { // Request was cancelled by client
				return
			}
		}
		served++
	})
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go s.ServeHTTP(w, req)
	}

	started.Wait()
	s.Drain(time.Millisecond * 10)

	require.Equal(t, 0, served)
}

// Private helpers

type testServiceStateChangeConsumer struct {
	called bool
}

func (c *testServiceStateChangeConsumer) StateChanged(service *Service) {
	c.called = true
}
