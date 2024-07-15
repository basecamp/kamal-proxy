package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBufferMiddleware(t *testing.T) {
	sendRequest := func(body string) *httptest.ResponseRecorder {
		middleware := WithBufferMiddleware(8, 4, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(w, r.Body)
		}))

		req := httptest.NewRequest("POST", "http://app.example.com/somepath", strings.NewReader(body))
		rec := httptest.NewRecorder()

		middleware.ServeHTTP(rec, req)
		return rec
	}

	t.Run("success", func(t *testing.T) {
		w := sendRequest("hello")

		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Equal(t, "hello", w.Body.String())
	})

	t.Run("body too large", func(t *testing.T) {
		w := sendRequest("this request body is much too large")

		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
	})
}
