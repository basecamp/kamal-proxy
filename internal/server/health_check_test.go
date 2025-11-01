package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	longTimeout  = time.Millisecond * 20
	shortTimeout = time.Millisecond * 10
)

func TestHealthCheck(t *testing.T) {
	run := func(t *testing.T, path string, expected []bool) {
		serverURL := testHealthCheckTarget(t, "")
		consumer := make(mockHealthCheckConsumer)

		serverURL.Path = path

		hc := NewHealthCheck(consumer, serverURL, shortTimeout, shortTimeout, "")
		t.Cleanup(hc.Close)

		for _, exp := range expected {
			result := <-consumer
			assert.Equal(t, exp, result)
		}
	}

	t.Run("Success", func(t *testing.T) {
		run(t, "", []bool{true})
	})

	t.Run("Success after retrying multiple attempts", func(t *testing.T) {
		run(t, "/retrying", []bool{false, false, true})
	})

	t.Run("Endpoint timing out", func(t *testing.T) {
		run(t, "/slow", []bool{false})
	})

	t.Run("Endpoint error", func(t *testing.T) {
		run(t, "/error", []bool{false})
	})
}

func TestHealthCheckWithCustomHost(t *testing.T) {
	t.Run("Custom Host header is sent", func(t *testing.T) {
		customHost := "example.com"
		serverURL := testHealthCheckTarget(t, customHost)
		consumer := make(mockHealthCheckConsumer)

		hc := NewHealthCheck(consumer, serverURL, shortTimeout, shortTimeout, customHost)
		t.Cleanup(hc.Close)

		result := <-consumer
		assert.True(t, result, "Health check should succeed with correct Host header")
	})

	t.Run("Health check fails with incorrect Host header", func(t *testing.T) {
		expectedHost := "example.com"
		wrongHost := "wrong.com"
		serverURL := testHealthCheckTarget(t, expectedHost)
		consumer := make(mockHealthCheckConsumer)

		hc := NewHealthCheck(consumer, serverURL, shortTimeout, shortTimeout, wrongHost)
		t.Cleanup(hc.Close)

		result := <-consumer
		assert.False(t, result, "Health check should fail when Host header doesn't match")
	})

	t.Run("Empty Host header uses default behavior", func(t *testing.T) {
		serverURL := testHealthCheckTarget(t, "")
		consumer := make(mockHealthCheckConsumer)

		hc := NewHealthCheck(consumer, serverURL, shortTimeout, shortTimeout, "")
		t.Cleanup(hc.Close)

		result := <-consumer
		assert.True(t, result, "Health check should succeed with default Host header")
	})
}

// Mocks

type mockHealthCheckConsumer chan bool

func (m mockHealthCheckConsumer) HealthCheckCompleted(success bool) {
	m <- success
}

// Helpers

func testHealthCheckTarget(t testing.TB, expectedHost string) *url.URL {
	t.Helper()

	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Host header if expectedHost is set
		if expectedHost != "" && r.Host != expectedHost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch r.URL.Path {
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "/retrying":
			attempts++
			if attempts < 3 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
		case "/slow":
			time.Sleep(longTimeout)
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	serverURL, _ := url.Parse(server.URL)
	return serverURL
}
