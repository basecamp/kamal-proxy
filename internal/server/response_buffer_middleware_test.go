package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResponseBufferMiddleware(t *testing.T) {
	t.Parallel()

	sendRequest := func(requestBody, responseBody string) *httptest.ResponseRecorder {
		middleware := WithResponseBufferMiddleware(4, 8, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(responseBody))
		}))

		req := httptest.NewRequest("POST", "http://app.example.com/somepath", strings.NewReader(requestBody))
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)
		return rec
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		w := sendRequest("hello", "ok")

		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("response body too large", func(t *testing.T) {
		t.Parallel()

		w := sendRequest("hello", "this response body is much too large")

		assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	})
}
