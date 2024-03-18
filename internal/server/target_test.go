package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"nhooyr.io/websocket"
)

func TestTarget_Serve(t *testing.T) {
	_, target := testBackend(t, "ok", http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
}

func TestTarget_ServeWebSocket(t *testing.T) {
	_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		require.NoError(t, err)
		defer c.CloseNow()

		c.Write(context.Background(), websocket.MessageText, []byte("hello"))
	})

	server := httptest.NewServer(target)
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

	_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		requestTarget = r.Host
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "custom.example.com"
	w := httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "custom.example.com", requestTarget)
}

func TestTarget_HeadersAreCorrectlyPreserved(t *testing.T) {
	var (
		xForwardedFor   string
		xForwardedProto string
		customHeader    string
	)

	_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
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
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "1.2.3.4, "+clientIP, xForwardedFor)
	require.Equal(t, "http", xForwardedProto)
	require.Equal(t, "Custom value", customHeader)

	// Adding X-Forwarded-For if the original does not have one
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, clientIP, xForwardedFor)
}

func TestTarget_AddedTargetBecomesHealthy(t *testing.T) {
	_, target := testBackend(t, "ok", http.StatusOK)

	target.BeginHealthChecks()

	require.True(t, target.WaitUntilHealthy(time.Second))
	require.Equal(t, TargetStateHealthy, target.state)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "ok", string(w.Body.String()))
}

func TestTarget_DrainWhenEmpty(t *testing.T) {
	_, target := testBackend(t, "ok", http.StatusOK)

	target.Drain(time.Second)
}

func TestTarget_DrainRequestsThatCompleteWithinTimeout(t *testing.T) {
	n := 3
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		time.Sleep(time.Millisecond * 200)
		served++
	})

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go target.ServeHTTP(w, req)
	}

	started.Wait()
	target.Drain(time.Second)

	require.Equal(t, n, served)
}

func TestTarget_DrainRequestsThatNeedToBeCancelled(t *testing.T) {
	n := 20
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		for i := 0; i < 500; i++ {
			time.Sleep(time.Millisecond * 10)
			if r.Context().Err() != nil { // Request was cancelled by client
				return
			}
		}
		served++
	})

	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go target.ServeHTTP(w, req)
	}

	started.Wait()
	target.Drain(time.Millisecond * 10)

	require.Equal(t, 0, served)
}

func TestTarget_RedirectToHTTPWhenTLSRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, TargetOptions{RequireTLS: true})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestTarget_EnforceMaxRequestBodySize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, TargetOptions{MaxRequestBodySize: 10})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Something longer than 10!"))
	w = httptest.NewRecorder()
	target.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
}
