package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestIDMiddleware_AddsAnIDWhenNotPresent(t *testing.T) {
	t.Parallel()

	handler := WithRequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		assert.NotEmpty(t, id)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequestIDMiddleware_PreservesExistingHeaderWhenPresent(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		assert.Equal(t, "1234", id)
	})
	handler := WithRequestIDMiddleware(next)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-ID", "1234")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}
