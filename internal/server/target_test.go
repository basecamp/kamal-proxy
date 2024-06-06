package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nhooyr.io/websocket"
)

func TestTarget_Serve(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
}

func TestTarget_ServeWebSocket(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		require.NoError(t, err)
		defer c.CloseNow()

		c.Write(context.Background(), websocket.MessageText, []byte("hello"))
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, err := target.StartRequest(r)
		require.NoError(t, err)
		target.SendRequest(w, r)
	}))
	defer server.Close()

	websocketURL := strings.Replace(server.URL, "http:", "ws:", 1)

	c, _, err := websocket.Dial(context.Background(), websocketURL, nil)
	require.NoError(t, err)
	defer c.CloseNow()

	kind, body, err := c.Read(context.Background())
	require.NoError(t, err)
	assert.Equal(t, websocket.MessageText, kind)
	assert.Equal(t, "hello", string(body))
}

func TestTarget_PreserveTargetHeader(t *testing.T) {
	var requestTarget string

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		requestTarget = r.Host
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "custom.example.com"
	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "custom.example.com", requestTarget)
}

func TestTarget_HeadersAreCorrectlyPreserved(t *testing.T) {
	var (
		xForwardedFor   string
		xForwardedProto string
		customHeader    string
	)

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		xForwardedFor = r.Header.Get("X-Forwarded-For")
		xForwardedProto = r.Header.Get("X-Forwarded-Proto")
		customHeader = r.Header.Get("Custom-Header")
	})

	// Preserving headers where X-Forwarded-For exists
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Custom-Header", "Custom value")

	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "1.2.3.4, "+clientIP, xForwardedFor)
	require.Equal(t, "http", xForwardedProto)
	require.Equal(t, "Custom value", customHeader)

	// Adding X-Forwarded-For if the original does not have one
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, clientIP, xForwardedFor)
}

func TestTarget_UnparseableQueryParametersArePreserved(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "p1=a;b;c&p2=%x&p3=ok", r.URL.RawQuery)
	})

	req := httptest.NewRequest(http.MethodGet, "/test?p1=a;b;c&p2=%x&p3=ok", nil)
	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestTarget_AddedTargetBecomesHealthy(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	target.BeginHealthChecks()

	require.True(t, target.WaitUntilHealthy(time.Second))
	require.Equal(t, TargetStateHealthy, target.state)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
}

func TestTarget_DrainWhenEmpty(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})

	target.Drain(time.Second)
}

func TestTarget_DrainRequestsThatCompleteWithinTimeout(t *testing.T) {
	n := 3
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 200)
		served++
		started.Done()
	})

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go testServeRequestWithTarget(t, target, w, req)
	}

	started.Wait()
	target.Drain(time.Second * 5)

	require.Equal(t, n, served)
}

func TestTarget_DrainRequestsThatNeedToBeCancelled(t *testing.T) {
	n := 20
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		for i := 0; i < 500; i++ {
			time.Sleep(time.Millisecond * 100)
			if r.Context().Err() != nil { // Request was cancelled by client
				return
			}
		}
		served++
	})

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go testServeRequestWithTarget(t, target, w, req)
	}

	started.Wait()
	target.Drain(time.Millisecond * 10)

	require.Equal(t, 0, served)
}

func TestTarget_DrainHijackedConnectionsImmediately(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		require.NoError(t, err)
		defer c.CloseNow()

		_, _, err = c.Read(context.Background())
		require.Error(t, err)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, err := target.StartRequest(r)
		require.NoError(t, err)
		target.SendRequest(w, r)
	}))
	defer server.Close()

	websocketURL := strings.Replace(server.URL, "http:", "ws:", 1)

	c, _, err := websocket.Dial(context.Background(), websocketURL, nil)
	require.NoError(t, err)
	defer c.CloseNow()

	startedDraining := time.Now()
	target.Drain(time.Second * 5)
	assert.Less(t, time.Since(startedDraining).Seconds(), 1.0)
}

func TestTarget_RequestTimeout(t *testing.T) {
	_, targetURL := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 200)
	})

	target, err := NewTarget(targetURL, defaultHealthCheckConfig, time.Millisecond*10, defaultResponseTimeout)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	started := time.Now()
	testServeRequestWithTarget(t, target, w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Result().StatusCode)
	assert.Less(t, time.Since(started).Seconds(), 100.0)
}

func TestTarget_RequestTimeoutWhenTheRequestCompletesInTime(t *testing.T) {
	_, targetURL := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 10)
	})

	target, err := NewTarget(targetURL, defaultHealthCheckConfig, time.Millisecond*200, defaultResponseTimeout)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	started := time.Now()
	testServeRequestWithTarget(t, target, w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Less(t, time.Since(started).Seconds(), 100.0)
}

func TestTarget_ZeroRequestTimeoutMeansNoTimeout(t *testing.T) {
	_, targetURL := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 10)
	})

	target, err := NewTarget(targetURL, defaultHealthCheckConfig, 0, defaultResponseTimeout)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	testServeRequestWithTarget(t, target, w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func testServeRequestWithTarget(t *testing.T, target *Target, w http.ResponseWriter, r *http.Request) {
	r, err := target.StartRequest(r)
	require.NoError(t, err)
	target.SendRequest(w, r)
}
