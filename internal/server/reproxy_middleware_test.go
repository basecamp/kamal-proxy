package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReproxyMiddleware_ReproxiesOnNonSuccessWithReproxyHeader(t *testing.T) {
	callCount := 0
	var requestedURLs []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		requestedURLs = append(requestedURLs, r.URL.String())

		assert.Equal(t, "true", r.Header.Get("X-Kamal-Reproxy"))

		bodyBytes, _ := io.ReadAll(r.Body)
		assert.Equal(t, "test body", string(bodyBytes), "request body should be preserved across reproxies")

		if callCount == 1 {
			w.Header().Set("X-Kamal-Reproxy-Location", "http://upstream2.example.com/path")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		} else {
			dest := r.Context().Value(contextKeyReproxyTo).(*url.URL)
			assert.Equal(t, "http://upstream2.example.com/path", dest.String(), "context should have reproxy destination")

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	})
	middleware := WithReproxyMiddleware("test-service", handler)

	body := NewRewindableReadCloser(io.NopCloser(strings.NewReader("test body")), 1024, 512)
	req := httptest.NewRequest("POST", "http://upstream1.example.com/path", body)

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	assert.Equal(t, 2, callCount, "handler should be called twice (initial + reproxy)")

	assert.Equal(t, "http://upstream1.example.com/path", requestedURLs[0], "first call should use original URL")
	assert.Equal(t, "http://upstream2.example.com/path", requestedURLs[1], "second call should use reproxy URL")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "success", w.Body.String())

	assert.Empty(t, w.Header().Get("X-Kamal-Reproxy-Location"))
}

func TestReproxyMiddleware_DoesNotReproxyOnSuccess(t *testing.T) {
	callCount := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("X-Kamal-Reproxy-Location", "http://upstream2.example.com/path")
		w.WriteHeader(http.StatusOK)
	})
	middleware := WithReproxyMiddleware("test-service", handler)

	req := httptest.NewRequest("GET", "http://upstream1.example.com/path", nil)
	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	assert.Equal(t, 1, callCount, "successful responses should not trigger reproxying")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReproxyMiddleware_MultipleReproxies(t *testing.T) {
	callCount := 0
	var requestedURLs []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		requestedURLs = append(requestedURLs, r.URL.String())

		switch callCount {
		case 1:
			w.Header().Set("X-Kamal-Reproxy-Location", "http://upstream2.example.com/path")
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.Header().Set("X-Kamal-Reproxy-Location", "http://upstream3.example.com/path")
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	})
	middleware := WithReproxyMiddleware("test-service", handler)

	body := NewRewindableReadCloser(io.NopCloser(strings.NewReader("test body")), 1024, 512)
	req := httptest.NewRequest("POST", "http://upstream1.example.com/path", body)

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	assert.Equal(t, 3, callCount, "handler should be called three times")
	assert.Equal(t, "http://upstream1.example.com/path", requestedURLs[0])
	assert.Equal(t, "http://upstream2.example.com/path", requestedURLs[1])
	assert.Equal(t, "http://upstream3.example.com/path", requestedURLs[2])
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReproxyMiddleware_ExceedsReproxyLimit(t *testing.T) {
	callCount := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("X-Kamal-Reproxy-Location", "http://upstream.example.com/path")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	middleware := WithReproxyMiddleware("test-service", handler)

	body := NewRewindableReadCloser(io.NopCloser(strings.NewReader("test body")), 1024, 512)
	req := httptest.NewRequest("POST", "http://upstream.example.com/path", body)

	// Mock the sleep function to track calls and avoid actual delays in test
	sleepCallCount := 0
	var sleepDurations []time.Duration
	originalSleep := reproxySleepFunc
	reproxySleepFunc = func(d time.Duration) {
		sleepCallCount++
		sleepDurations = append(sleepDurations, d)
	}
	t.Cleanup(func() { reproxySleepFunc = originalSleep })

	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, req)

	assert.Equal(t, 30, callCount, "handler should be called exactly 30 times (the reproxy limit)")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	// Check throttling behaviour
	assert.Equal(t, 27, sleepCallCount, "sleep should be called 27 times (attempts 3-29)")
	for _, d := range sleepDurations {
		assert.Equal(t, time.Second, d, "each sleep should be 1 second")
	}
}
