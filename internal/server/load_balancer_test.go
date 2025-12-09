package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetList_NewTargetListIllegalNames(t *testing.T) {
	_, err := NewTargetList([]string{"", "_", "/"}, []string{}, TargetOptions{})
	assert.Error(t, err, ErrorInvalidHostPattern)
}

func TestTargetList_Names(t *testing.T) {
	tl, err := NewTargetList([]string{"one", "two", "three"}, []string{}, TargetOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"one", "two", "three"}, tl.Names())
}

func TestLoadBalancer_Targets(t *testing.T) {
	tl, err := NewTargetList([]string{"one", "two", "three"}, []string{}, defaultTargetOptions)
	require.NoError(t, err)

	lb := NewLoadBalancer(tl, DefaultWriterAffinityTimeout, false)
	t.Cleanup(lb.Dispose)

	assert.Equal(t, []string{"one", "two", "three"}, lb.Targets().Names())
}

func TestLoadBalancer_WaitUntilHealthy(t *testing.T) {
	lb := testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.Error(t, lb.WaitUntilHealthy(time.Millisecond*5), ErrorTargetFailedToBecomeHealthy)

	lb = testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) {},
		func(w http.ResponseWriter, r *http.Request) {},
	)
	require.NoError(t, lb.WaitUntilHealthy(time.Second))
}

func TestLoadBalancer_StartRequest(t *testing.T) {
	lb := testLoadBalancerWithHandlers(t,
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("one"))
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("two"))
		},
	)
	require.NoError(t, lb.WaitUntilHealthy(time.Second))

	bodies := []string{}
	for range 4 {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		lb.StartRequest(w, r)()

		assert.Equal(t, http.StatusOK, w.Code)
		bodies = append(bodies, w.Body.String())
	}

	assert.Contains(t, bodies, "one")
	assert.Contains(t, bodies, "two")
}

func TestLoadBalancer_Readers(t *testing.T) {
	createLoadBalancer := func(includeReader bool, writerAffinityTimeout time.Duration, readTargetsAcceptWebsockets bool, handler http.HandlerFunc) *LoadBalancer {
		writer := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Writer", "true")
			if handler != nil {
				handler(w, r)
			}
		})

		readers := []string{}
		if includeReader {
			reader := testReadOnlyTarget(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Writer", "false")
			})
			readers = []string{reader.Address()}
		}

		tl, err := NewTargetList([]string{writer.Address()}, readers, defaultTargetOptions)
		require.NoError(t, err)

		lb := NewLoadBalancer(tl, writerAffinityTimeout, readTargetsAcceptWebsockets)
		t.Cleanup(lb.Dispose)

		lb.WaitUntilHealthy(time.Second)

		return lb
	}

	createDefaultLoadBalancer := func(includeReader bool) *LoadBalancer {
		return createLoadBalancer(includeReader, DefaultWriterAffinityTimeout, false, nil)
	}

	checkResponse := func(lb *LoadBalancer, r *http.Request, writer bool) *httptest.ResponseRecorder {
		t.Helper()

		var w *httptest.ResponseRecorder
		// Mutliple requests to ensure we aren't cycling between the targets
		for range 2 {
			w = httptest.NewRecorder()
			lb.StartRequest(w, r)()
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, strconv.FormatBool(writer), w.Header().Get("X-Writer"))
		}

		return w
	}

	isWriter := true
	isReader := false

	t.Run("routing read and write requests", func(t *testing.T) {
		lb := createDefaultLoadBalancer(true)

		_ = checkResponse(lb, httptest.NewRequest("GET", "/", nil), isReader)
		_ = checkResponse(lb, httptest.NewRequest("HEAD", "/", nil), isReader)

		_ = checkResponse(lb, httptest.NewRequest("DELETE", "/", nil), isWriter)
		_ = checkResponse(lb, httptest.NewRequest("PATCH", "/", nil), isWriter)
		_ = checkResponse(lb, httptest.NewRequest("POST", "/", nil), isWriter)
		_ = checkResponse(lb, httptest.NewRequest("PUT", "/", nil), isWriter)
	})

	t.Run("routing read requests when no readers", func(t *testing.T) {
		lb := createDefaultLoadBalancer(false)

		_ = checkResponse(lb, httptest.NewRequest("GET", "/", nil), isWriter)
		_ = checkResponse(lb, httptest.NewRequest("HEAD", "/", nil), isWriter)
	})

	t.Run("WebSocket requests are routed to writers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")

		lb := createDefaultLoadBalancer(true)
		_ = checkResponse(lb, req, isWriter)
	})

	t.Run("WebSocket requests can optionally be routed to readers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")

		lb := createLoadBalancer(true, DefaultWriterAffinityTimeout, true, nil)
		_ = checkResponse(lb, req, isReader)
	})

	t.Run("writer affinity sub 1s", func(t *testing.T) {
		lb := createLoadBalancer(true, time.Millisecond*200, false, nil)

		w := checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), isWriter)
		cookie := w.Result().Cookies()[0]
		assert.Equal(t, LoadBalancerWriteCookieName, cookie.Name)
		assert.Greater(t, cookie.Expires, time.Now())
	})

	t.Run("writer affinity", func(t *testing.T) {
		lb := createDefaultLoadBalancer(true)

		w := checkResponse(lb, httptest.NewRequest("GET", "/", nil), false)
		assert.Empty(t, w.Result().Cookies())

		w = checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), isWriter)
		cookie := w.Result().Cookies()[0]
		assert.Equal(t, LoadBalancerWriteCookieName, cookie.Name)

		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(cookie)
		_ = checkResponse(lb, req, isWriter)
	})

	t.Run("writer affinity not active when no readers", func(t *testing.T) {
		lb := createDefaultLoadBalancer(false)

		w := checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), isWriter)
		assert.Empty(t, w.Result().Cookies())
	})

	t.Run("writer affinity not active when the timeout is zero", func(t *testing.T) {
		lb := createLoadBalancer(true, 0, false, nil)

		w := checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), isWriter)
		assert.Empty(t, w.Result().Cookies())
	})

	t.Run("writer affinity not active when `X-Writer-Affinity` response header is `false`", func(t *testing.T) {
		lb := createLoadBalancer(true, DefaultWriterAffinityTimeout, false, func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Writer-Affinity", "false")
		})

		req := httptest.NewRequest("PUT", "/something", nil)

		w := checkResponse(lb, req, isWriter)
		assert.Empty(t, w.Result().Cookies())
		assert.Empty(t, w.Header().Get("X-Writer-Affinity"))
	})
}

func TestLoadBalancer_TargetHeader(t *testing.T) {
	reader := testReadOnlyTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(LoadBalancerTargetHeader, r.Header.Get(LoadBalancerTargetHeader))
	})

	writer := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(LoadBalancerTargetHeader, r.Header.Get(LoadBalancerTargetHeader))
	})

	tl, _ := NewTargetList([]string{writer.Address()}, []string{reader.Address()}, defaultTargetOptions)
	lb := NewLoadBalancer(tl, DefaultWriterAffinityTimeout, false)
	t.Cleanup(lb.Dispose)

	lb.WaitUntilHealthy(time.Second)

	checkHeader := func(method string, expected string, priorHeader ...string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/", nil)
		if len(priorHeader) > 0 {
			req.Header.Set(LoadBalancerTargetHeader, priorHeader[0])
		}

		lb.StartRequest(w, req)()

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, expected, w.Header().Get(LoadBalancerTargetHeader))
	}

	checkHeader("GET", reader.Address())
	checkHeader("POST", writer.Address())
	checkHeader("POST", writer.Address(), "existing")

	for _, t := range tl {
		t.options.ForwardHeaders = true
	}
	checkHeader("POST", "existing, "+writer.Address(), "existing")
}

// Helpers

func testLoadBalancerWithHandlers(t *testing.T, handlers ...http.HandlerFunc) *LoadBalancer {
	targets := []string{}
	for _, h := range handlers {
		targets = append(targets, testTarget(t, h).Address())
	}

	tl, err := NewTargetList(targets, []string{}, defaultTargetOptions)
	require.NoError(t, err)

	lb := NewLoadBalancer(tl, DefaultWriterAffinityTimeout, false)
	t.Cleanup(lb.Dispose)

	return lb
}
