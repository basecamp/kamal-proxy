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
		serverURL := testHealthCheckTarget(t)
		consumer := make(mockHealthCheckConsumer)

		serverURL.Path = path

		hc := NewHealthCheck(consumer, serverURL, shortTimeout, shortTimeout)
		defer hc.Close()

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

// Mocks

type mockHealthCheckConsumer chan bool

func (m mockHealthCheckConsumer) HealthCheckCompleted(success bool) {
	m <- success
}

// Helpers

func testHealthCheckTarget(t testing.TB) *url.URL {
	t.Helper()

	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(server.Close)

	serverURL, _ := url.Parse(server.URL)
	return serverURL
}
