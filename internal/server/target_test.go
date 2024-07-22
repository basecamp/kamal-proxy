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
	sendWebsocketMessage := func(buffer bool, body string) (websocket.MessageType, []byte, error) {
		targetOptions := TargetOptions{
			BufferRequests:      buffer,
			MaxMemoryBufferSize: 1,
			MaxRequestBodySize:  2,
			MaxResponseBodySize: 2,
			HealthCheckConfig:   defaultHealthCheckConfig,
		}

		target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
			require.NoError(t, err)

			go func() {
				kind, body, err := c.Read(context.Background())
				require.NoError(t, err)
				assert.Equal(t, websocket.MessageText, kind)

				c.Write(context.Background(), websocket.MessageText, body)
				defer c.CloseNow()
			}()
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

		c.Write(context.Background(), websocket.MessageText, []byte(body))

		return c.Read(context.Background())
	}

	t.Run("without buffering", func(t *testing.T) {
		kind, body, err := sendWebsocketMessage(false, "hello")
		require.NoError(t, err)
		assert.Equal(t, websocket.MessageText, kind)
		assert.Equal(t, "hello", string(body))
	})

	t.Run("with buffering", func(t *testing.T) {
		kind, body, err := sendWebsocketMessage(true, "world")
		require.NoError(t, err)
		assert.Equal(t, websocket.MessageText, kind)
		assert.Equal(t, "world", string(body))
	})
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

func TestTarget_IsHealthCheckRequest(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})

	assert.True(t, target.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up", nil)))
	assert.True(t, target.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up?one=two", nil)))

	assert.False(t, target.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up/other", nil)))
	assert.False(t, target.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/health", nil)))
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

func TestTarget_EnforceMaxBodySizes(t *testing.T) {
	sendRequest := func(buffer bool, maxMemorySize int64, maxBodySize int64, requestBody, responseBody string) *httptest.ResponseRecorder {
		targetOptions := TargetOptions{
			BufferRequests:      buffer,
			MaxMemoryBufferSize: maxMemorySize,
			MaxRequestBodySize:  maxBodySize,
			MaxResponseBodySize: maxBodySize,
			HealthCheckConfig:   defaultHealthCheckConfig,
		}
		target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(responseBody))
		})

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(requestBody))
		w := httptest.NewRecorder()

		testServeRequestWithTarget(t, target, w, req)
		return w
	}

	t.Run("without buffering", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(false, 1, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, 1, 10, "request limits are not enforced when not buffering", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, 1, 10, "hello", "response limits are not enforced when not buffering")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "response limits are not enforced when not buffering", string(w.Body.String()))
		})
	})

	t.Run("with buffering but no additional disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, 10, 10, "hello", "this response is too large")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})

	t.Run("with buffering and a separate disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, 5, 20, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, 5, 20, "hello hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, 5, 20, "hello hello hello hello hello", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})
		t.Run("response too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, 5, 20, "hello", "hello hello")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "hello hello", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, 5, 20, "hello", "this is even longer than the disk limit")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})
}

func testServeRequestWithTarget(t *testing.T, target *Target, w http.ResponseWriter, r *http.Request) {
	r, err := target.StartRequest(r)
	require.NoError(t, err)
	target.SendRequest(w, r)
}
