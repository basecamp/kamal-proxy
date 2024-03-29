package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestIDMiddleware_adds_an_id_when_not_present(t *testing.T) {
	handler := WithRequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		assert.NotEmpty(t, id)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequestIDMiddleware_preserves_existing_header_when_present(t *testing.T) {
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
