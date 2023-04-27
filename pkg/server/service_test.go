package server

import (
	"net"
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

func TestService_PreserveHostHeader(t *testing.T) {
	var requestHost string

	_, host := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		requestHost = r.Host
	})
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "custom.example.com"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "custom.example.com", requestHost)
}

func TestService_HeadersAreCorrectlyPreserved(t *testing.T) {
	var (
		xForwardedFor   string
		xForwardedProto string
		customHeader    string
	)

	_, host := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		xForwardedFor = r.Header.Get("X-Forwarded-For")
		xForwardedProto = r.Header.Get("X-Forwarded-Proto")
		customHeader = r.Header.Get("Custom-Header")
	})
	hostURL, _ := host.ToURL()

	s := NewService(hostURL, defaultHealthCheckConfig)

	// Preserving headers where X-Forwarded-For exists
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Custom-Header", "Custom value")

	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "1.2.3.4, "+clientIP, xForwardedFor)
	require.Equal(t, "http", xForwardedProto)
	require.Equal(t, "Custom value", customHeader)

	// Adding X-Forwarded-For if the original does not have one
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	s.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, clientIP, xForwardedFor)
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
