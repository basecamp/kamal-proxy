package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorPageMiddleware(t *testing.T) {
	check := func(handler http.HandlerFunc) (int, string, string) {
		middleware := WithErrorPageMiddleware(handler)

		req := httptest.NewRequest("GET", "http://example.com", nil)
		resp := httptest.NewRecorder()

		middleware.ServeHTTP(resp, req)

		return resp.Result().StatusCode, resp.Header().Get("Content-Type"), resp.Body.String()
	}

	t.Run("When setting a custom error response", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusNotFound, nil)
		})

		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Not Found", body)
	})

	t.Run("When including template arguments in a custom error response", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusServiceUnavailable, struct{ Message string }{"Gone to lunch"})
		})

		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Service Temporarily Unavailable", body)
		assert.Regexp(t, "Gone to lunch", body)
	})

	t.Run("When trying to set an error that we don't have a template for", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusTeapot, nil)
		})

		assert.Equal(t, http.StatusTeapot, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "I'm a teapot", body)
	})

	t.Run("When the backend returns an error normally", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusTeapot), http.StatusTeapot)
		})

		assert.Equal(t, http.StatusTeapot, status)
		assert.Equal(t, "text/plain; charset=utf-8", contentType)
		assert.Regexp(t, "I'm a teapot", body)
	})
}
