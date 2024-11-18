package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestStartMiddleware_AddsUnixMilliWhenNotPresent(t *testing.T) {
	handler := WithRequestStartMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timestamp := r.Header.Get(requestStartHeader)
		assert.NotEmpty(t, timestamp)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequestStartMiddleware_PreservesExistingHeaderWhenPresent(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timestamp := r.Header.Get(requestStartHeader)
		assert.Equal(t, "1234", timestamp)
	})
	handler := WithRequestStartMiddleware(next)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(requestStartHeader, "1234")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}
