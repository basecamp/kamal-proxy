package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestStartMiddleware_AddsUnixMilliWhenNotPresent(t *testing.T) {
	handler := WithRequestStartMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerValue := r.Header.Get("X-Request-Start")

		assert.Regexp(t, `^t=\d+$`, headerValue, "Value should be formatted as `t=<timestamp>`")

		requestStartMilli, _ := strconv.ParseInt(headerValue[2:], 10, 64)
		requestStart := time.UnixMilli(requestStartMilli)
		assert.WithinDuration(t, time.Now(), requestStart, time.Second)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequestStartMiddleware_PreservesExistingHeaderWhenPresent(t *testing.T) {
	handler := WithRequestStartMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "t=1234", r.Header.Get("X-Request-Start"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(requestStartHeader, "t=1234")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}
