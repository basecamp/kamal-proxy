package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestTarget_ServeSSE(t *testing.T) {
	receiveSSEMessage := func(bufferRequests, bufferResponses bool) (string, error) {
		finishedReading := make(chan struct{})

		targetOptions := TargetOptions{
			BufferRequests:      bufferRequests,
			BufferResponses:     bufferResponses,
			MaxMemoryBufferSize: DefaultMaxMemoryBufferSize,
			HealthCheckConfig:   defaultHealthCheckConfig,
		}

		target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: hello\n\n"))
			w.(http.Flusher).Flush()

			// Don't return until the client has finished reading. Fail the test if this takes too long.
			select {
			case <-finishedReading:
				break
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for client to finish reading")
			}
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, err := target.StartRequest(r)
			require.NoError(t, err)
			target.SendRequest(w, r)
		}))
		t.Cleanup(server.Close)
		t.Cleanup(func() { close(finishedReading) })

		resp, err := http.Get(server.URL)
		require.NoError(t, err)

		scanner := bufio.NewScanner(resp.Body)
		if !scanner.Scan() {
			return "", scanner.Err()
		}

		return scanner.Text(), nil
	}

	t.Run("without buffering", func(t *testing.T) {
		message, err := receiveSSEMessage(false, false)
		require.NoError(t, err)
		assert.Equal(t, "data: hello", message)
	})

	t.Run("with buffering", func(t *testing.T) {
		message, err := receiveSSEMessage(true, true)
		require.NoError(t, err)
		assert.Equal(t, "data: hello", message)
	})
}

func TestTarget_ServeWebSocket(t *testing.T) {
	sendWebsocketMessage := func(bufferRequests, bufferResponses bool, body string) (websocket.MessageType, []byte, error) {
		targetOptions := TargetOptions{
			BufferRequests:      bufferRequests,
			BufferResponses:     bufferResponses,
			MaxMemoryBufferSize: 1,
			MaxRequestBodySize:  2,
			MaxResponseBodySize: 2,
			HealthCheckConfig:   defaultHealthCheckConfig,
		}

		target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
			require.NoError(t, err)

			go func(t *testing.T) {
				kind, body, err := c.Read(t.Context())
				require.NoError(t, err)
				assert.Equal(t, websocket.MessageText, kind)

				c.Write(t.Context(), websocket.MessageText, body)
				defer c.CloseNow()
			}(t)
		})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, err := target.StartRequest(r)
			require.NoError(t, err)
			target.SendRequest(w, r)
		}))
		t.Cleanup(server.Close)

		websocketURL := strings.Replace(server.URL, "http:", "ws:", 1)

		c, _, err := websocket.Dial(t.Context(), websocketURL, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = c.CloseNow() })

		c.Write(t.Context(), websocket.MessageText, []byte(body))

		return c.Read(t.Context())
	}

	t.Run("without buffering", func(t *testing.T) {
		kind, body, err := sendWebsocketMessage(false, false, "hello")
		require.NoError(t, err)
		assert.Equal(t, websocket.MessageText, kind)
		assert.Equal(t, "hello", string(body))
	})

	t.Run("with buffering", func(t *testing.T) {
		kind, body, err := sendWebsocketMessage(true, true, "world")
		require.NoError(t, err)
		assert.Equal(t, websocket.MessageText, kind)
		assert.Equal(t, "world", string(body))
	})
}

func TestTarget_CancelledRequestsHaveStatus499(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})

	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, 499, w.Result().StatusCode)
	require.Empty(t, string(w.Body.String()))
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

func TestTarget_XForwardedHeadersPopulatedByDefault(t *testing.T) {
	var (
		xForwardedFor   string
		xForwardedProto string
		xForwardedHost  string
		customHeader    string
	)

	targetOptions := TargetOptions{ForwardHeaders: false}
	target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
		xForwardedFor = r.Header.Get("X-Forwarded-For")
		xForwardedProto = r.Header.Get("X-Forwarded-Proto")
		xForwardedHost = r.Header.Get("X-Forwarded-Host")
		customHeader = r.Header.Get("Custom-Header")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// These values should be untrusted and cleared
	req.Header.Set("X-Forwarded-For", "10.10.10.10")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "untrusted.example.com")

	// Other headers should be preserved
	req.Header.Set("Custom-Header", "Custom value")

	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, clientIP, xForwardedFor)
	require.Equal(t, "http", xForwardedProto)
	require.Equal(t, "example.com", xForwardedHost)
	require.Equal(t, "Custom value", customHeader)
}

func TestTarget_XForwardedHeadersCanBeForwarded(t *testing.T) {
	var (
		xForwardedFor   string
		xForwardedProto string
		xForwardedHost  string
		customHeader    string
	)

	targetOptions := TargetOptions{ForwardHeaders: true}
	target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
		xForwardedFor = r.Header.Get("X-Forwarded-For")
		xForwardedProto = r.Header.Get("X-Forwarded-Proto")
		xForwardedHost = r.Header.Get("X-Forwarded-Host")
		customHeader = r.Header.Get("Custom-Header")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// These headers should all be trusted and forwarded
	req.Header.Set("X-Forwarded-For", "10.10.10.10")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "untrusted.example.com")
	req.Header.Set("Custom-Header", "Custom value")

	clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, "10.10.10.10, "+clientIP, xForwardedFor)
	require.Equal(t, "https", xForwardedProto)
	require.Equal(t, "untrusted.example.com", xForwardedHost)
	require.Equal(t, "Custom value", customHeader)

	// Headers will still be populated as usual if they are not present
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	testServeRequestWithTarget(t, target, w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	require.Equal(t, clientIP, xForwardedFor)
	require.Equal(t, "http", xForwardedProto)
	require.Equal(t, "example.com", xForwardedHost)
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

	assert.True(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up", nil)))
	assert.True(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodHead, "/up", nil)))
	assert.True(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up?one=two", nil)))
	assert.True(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodHead, "/up?one=two", nil)))

	assert.False(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/up/other", nil)))
	assert.False(t, target.options.IsHealthCheckRequest(httptest.NewRequest(http.MethodGet, "/health", nil)))
}

func TestTarget_AddedTargetBecomesHealthy(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	waiter := newTestHealthConsumer()
	target.BeginHealthChecks(waiter)

	require.NoError(t, waiter.waitUntilHealthy())
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
	var served atomic.Uint32

	var started sync.WaitGroup
	started.Add(n)

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 200)
		served.Add(1)
		started.Done()
	})

	for range n {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go testServeRequestWithTarget(t, target, w, req)
	}

	started.Wait()
	target.Drain(time.Second * 5)

	require.Equal(t, uint32(n), served.Load())
}

func TestTarget_DrainRequestsThatNeedToBeCancelled(t *testing.T) {
	n := 20
	served := 0

	var started sync.WaitGroup
	started.Add(n)

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		started.Done()
		for range 500 {
			time.Sleep(time.Millisecond * 100)
			if r.Context().Err() != nil { // Request was cancelled by client
				return
			}
		}
		served++
	})

	for range n {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		go func() {
			testServeRequestWithTarget(t, target, w, req)
			assert.Equal(t, http.StatusGatewayTimeout, w.Result().StatusCode)
		}()
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

		_, _, err = c.Read(t.Context())
		require.Error(t, err)
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, err := target.StartRequest(r)
		require.NoError(t, err)
		target.SendRequest(w, r)
	}))
	t.Cleanup(server.Close)

	websocketURL := strings.Replace(server.URL, "http:", "ws:", 1)

	c, _, err := websocket.Dial(t.Context(), websocketURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseNow() })

	startedDraining := time.Now()
	target.Drain(time.Second * 5)
	assert.Less(t, time.Since(startedDraining).Seconds(), 1.0)
}

func TestTarget_EnforceMaxBodySizes(t *testing.T) {
	sendRequest := func(bufferRequests, bufferResponses bool, maxMemorySize, maxBodySize int64, requestBody, responseBody string) *httptest.ResponseRecorder {
		targetOptions := TargetOptions{
			BufferRequests:      bufferRequests,
			BufferResponses:     bufferResponses,
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
			w := sendRequest(false, false, 1, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, 1, 10, "request limits are not enforced when not buffering", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, 1, 10, "hello", "response limits are not enforced when not buffering")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "response limits are not enforced when not buffering", string(w.Body.String()))
		})
	})

	t.Run("with buffering but no additional disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, true, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, 10, 10, "hello", "this response is too large")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})

	t.Run("with buffering and a separate disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, true, 5, 20, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, true, 5, 20, "hello hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, 5, 20, "hello hello hello hello hello", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, true, 5, 20, "hello", "hello hello")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "hello hello", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, 5, 20, "hello", "this is even longer than the disk limit")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})

	t.Run("when buffering requests but not responses", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, false, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, false, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, false, 10, 10, "hello", "this response is very large")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "this response is very large", string(w.Body.String()))
		})
	})

	t.Run("when buffering responses but not requests", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(false, true, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, true, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, true, 10, 10, "hello", "this response is very large")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})
}

func TestTarget_MalformedChunkedEncodingWithoutBuffering(t *testing.T) {
	targetOptions := TargetOptions{
		BufferRequests:    false,
		HealthCheckConfig: defaultHealthCheckConfig,
	}

	target := testTargetWithOptions(t, targetOptions, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, err := target.StartRequest(r)
		require.NoError(t, err)
		target.SendRequest(w, r)
	}))
	t.Cleanup(server.Close)

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	rawRequest := "POST / HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"\r\n" +
		"ZZ\r\n"

	_, err = conn.Write([]byte(rawRequest))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTarget_buildHealthCheckURL(t *testing.T) {
	tests := []struct {
		name            string
		targetURL       string
		healthCheckPort int
		healthCheckPath string
		expectedURL     string
	}{
		{
			name:            "default port - same as target",
			targetURL:       "localhost:3000",
			healthCheckPort: 0,
			healthCheckPath: "/up",
			expectedURL:     "http://localhost:3000/up",
		},
		{
			name:            "custom health check port",
			targetURL:       "localhost:3000",
			healthCheckPort: 8080,
			healthCheckPath: "/health",
			expectedURL:     "http://localhost:8080/health",
		},
		{
			name:            "target without port, custom health check port",
			targetURL:       "localhost",
			healthCheckPort: 9090,
			healthCheckPath: "/status",
			expectedURL:     "http://localhost:9090/status",
		},
		{
			name:            "target without port, default health check",
			targetURL:       "localhost",
			healthCheckPort: 0,
			healthCheckPath: "/up",
			expectedURL:     "http://localhost/up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := TargetOptions{
				HealthCheckConfig: HealthCheckConfig{
					Path: tt.healthCheckPath,
					Port: tt.healthCheckPort,
				},
			}

			target, err := NewTarget(tt.targetURL, options)
			if err != nil {
				t.Fatalf("Failed to create target: %v", err)
			}

			healthCheckURL := target.buildHealthCheckURL()
			if healthCheckURL.String() != tt.expectedURL {
				t.Errorf("buildHealthCheckURL() = %v, want %v", healthCheckURL.String(), tt.expectedURL)
			}
		})
	}
}

// Helpers

func testServeRequestWithTarget(t *testing.T, target *Target, w http.ResponseWriter, r *http.Request) {
	r, err := target.StartRequest(r)
	require.NoError(t, err)
	target.SendRequest(w, r)
}

type testHealthConsumer struct {
	healthy chan struct{}
}

func newTestHealthConsumer() *testHealthConsumer {
	return &testHealthConsumer{
		healthy: make(chan struct{}),
	}
}

func (c *testHealthConsumer) TargetStateChanged(target *Target) {
	if target.state == TargetStateHealthy {
		close(c.healthy)
	}
}

func (c *testHealthConsumer) waitUntilHealthy() error {
	select {
	case <-c.healthy:
		return nil
	case <-time.After(5 * time.Second):
		return ErrorTargetFailedToBecomeHealthy
	}
}
