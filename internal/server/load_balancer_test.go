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
	defer lb.Dispose()

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
	createLoadBalancer := func(includeReader bool, writerAffinityTimeout time.Duration) *LoadBalancer {
		writer := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("writer"))
		})

		readers := []string{}
		if includeReader {
			reader := testReadOnlyTarget(t, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("reader"))
			})
			readers = []string{reader.Target()}
		}

		tl, err := NewTargetList([]string{writer.Target()}, readers, defaultTargetOptions)
		require.NoError(t, err)

		lb := NewLoadBalancer(tl, writerAffinityTimeout, false)
		t.Cleanup(lb.Dispose)

		lb.WaitUntilHealthy(time.Second)

		return lb
	}

	checkResponse := func(lb *LoadBalancer, r *http.Request, expected string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		lb.StartRequest(w, r)()
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, expected, w.Body.String())

		return w
	}

	t.Run("routing read and write requests", func(t *testing.T) {
		lb := createLoadBalancer(true, DefaultWriterAffinityTimeout)

		_ = checkResponse(lb, httptest.NewRequest("GET", "/", nil), "reader")
		_ = checkResponse(lb, httptest.NewRequest("GET", "/", nil), "reader")

		_ = checkResponse(lb, httptest.NewRequest("DELETE", "/", nil), "writer")
		_ = checkResponse(lb, httptest.NewRequest("PATCH", "/", nil), "writer")
		_ = checkResponse(lb, httptest.NewRequest("POST", "/", nil), "writer")
		_ = checkResponse(lb, httptest.NewRequest("PUT", "/", nil), "writer")
	})

	t.Run("writer affinity", func(t *testing.T) {
		lb := createLoadBalancer(true, DefaultWriterAffinityTimeout)

		w := checkResponse(lb, httptest.NewRequest("GET", "/", nil), "reader")
		assert.Empty(t, w.Result().Cookies())

		w = checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), "writer")
		cookie := w.Result().Cookies()[0]
		assert.Equal(t, LoadBalancerWriteCookieName, cookie.Name)

		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(cookie)
		_ = checkResponse(lb, req, "writer")
	})

	t.Run("writer affinity not active when no readers", func(t *testing.T) {
		lb := createLoadBalancer(false, DefaultWriterAffinityTimeout)

		w := checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), "writer")
		assert.Empty(t, w.Result().Cookies())
	})

	t.Run("writer affinity not active when the timeout is zero", func(t *testing.T) {
		lb := createLoadBalancer(true, 0)

		w := checkResponse(lb, httptest.NewRequest("PUT", "/something", nil), "writer")
		assert.Empty(t, w.Result().Cookies())
	})
}

// Helpers

func testLoadBalancerWithHandlers(t *testing.T, handlers ...http.HandlerFunc) *LoadBalancer {
	targets := []string{}
	for _, h := range handlers {
		targets = append(targets, testTarget(t, h).Target())
	}

	tl, err := NewTargetList(targets, []string{}, defaultTargetOptions)
	require.NoError(t, err)

	lb := NewLoadBalancer(tl, DefaultWriterAffinityTimeout, false)
	t.Cleanup(lb.Dispose)

	return lb
}
