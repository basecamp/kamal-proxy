package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBufferMiddleware(t *testing.T) {
	sendRequest := func(requestBody, responseBody string) *httptest.ResponseRecorder {
		middleware := WithBufferMiddleware(4, 8, 8, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(responseBody))
		}))

		req := httptest.NewRequest("POST", "http://app.example.com/somepath", strings.NewReader(requestBody))
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)
		return rec
	}

	t.Run("success", func(t *testing.T) {
		w := sendRequest("hello", "ok")

		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("request body too large", func(t *testing.T) {
		w := sendRequest("this request body is much too large", "ok")

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
	})

	t.Run("response body too large", func(t *testing.T) {
		w := sendRequest("hello", "this response body is much too large")

		assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	})
}
