package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/kevinmcconnell/mproxy/pkg/server"
)

func Test503WhenNoUpstreams(t *testing.T) {
	proxyServer := testProxyServer(t)
	resp, err := http.Get("http://" + proxyServer.Addr())
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestSingleUpstreamServesTraffic(t *testing.T) {
	n := 100

	var requested sync.WaitGroup
	var served atomic.Int64

	proxyServer := testProxyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/up" {
			served.Add(1)
		}
		json.NewEncoder(w).Encode("Hello")
	})

	requested.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			resp, err := http.Get("http://" + proxyServer.Addr())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var body string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, "Hello", body)

			requested.Done()
		}()
	}
	requested.Wait()

	require.Equal(t, n, int(served.Load()))
}

func Test502WhenUpstreamCrashes(t *testing.T) {
	proxyServer := testProxyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/up" {
			panic(true)
		}
	})
	resp, err := http.Get("http://" + proxyServer.Addr())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestMaxRequestBodySizeIsEnforced(t *testing.T) {
	proxyServer := testProxyServer(t, func(w http.ResponseWriter, r *http.Request) {})

	resp, err := http.Post("http://"+proxyServer.Addr(), "text/plain", bytes.NewReader(make([]byte, 100)))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Post("http://"+proxyServer.Addr(), "text/plain", bytes.NewReader(make([]byte, 1e6)))
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestMultipleUpstreamsShareTraffic(t *testing.T) {
	n := 100

	var requested sync.WaitGroup
	var s1, s2, s3 atomic.Int64

	proxyServer := testProxyServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/up" {
				s1.Add(1)
			}
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/up" {
				s2.Add(1)
			}
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/up" {
				s3.Add(1)
			}
		},
	)

	requested.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			resp, err := http.Get("http://" + proxyServer.Addr())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			requested.Done()
		}()
	}
	requested.Wait()

	require.Equal(t, n, int(s1.Load()+s2.Load()+s3.Load()))
	require.GreaterOrEqual(t, int(s1.Load()), n/3)
	require.GreaterOrEqual(t, int(s2.Load()), n/3)
	require.GreaterOrEqual(t, int(s3.Load()), n/3)
}

func TestWebsocketTraffic(t *testing.T) {
	upgrader := websocket.Upgrader{}
	proxyServer := testProxyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/up" {
			conn, err := upgrader.Upgrade(w, r, nil)
			require.NoError(t, err)

			var msg string
			require.NoError(t, conn.ReadJSON(&msg))
			require.Equal(t, "marco", msg)

			require.NoError(t, conn.WriteJSON("polo"))
		}
	})

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+proxyServer.Addr(), nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteJSON("marco"))

	var msg string
	require.NoError(t, conn.ReadJSON(&msg))
	require.Equal(t, "polo", msg)
}

// Helpers

func testProxyServer(t *testing.T, handlers ...http.HandlerFunc) *server.Server {
	dir, err := os.MkdirTemp("", "")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	proxyServer := server.NewServer(server.Config{
		SocketPath:         path.Join(dir, "mproxy.sock"),
		AddTimeout:         time.Second,
		DrainTimeout:       time.Second,
		MaxRequestBodySize: 1024,
	})
	err = proxyServer.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		proxyServer.Stop()
		os.RemoveAll(dir)
	})

	for _, handler := range handlers {
		upstream := httptest.NewServer(http.HandlerFunc(handler))

		upstreamURL, _ := url.Parse(upstream.URL)
		proxyServer.LoadBalancer().Add([]*url.URL{upstreamURL})

		t.Cleanup(func() {
			proxyServer.LoadBalancer().Remove([]*url.URL{upstreamURL})
			upstream.Close()
		})
	}

	return proxyServer
}
