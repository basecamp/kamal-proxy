package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResponseBufferMiddleware(t *testing.T) {
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
		w := sendRequest("hello", "ok")

		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("response body too large", func(t *testing.T) {
		w := sendRequest("hello", "this response body is much too large")

		assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	})
}

func TestResponseBufferMiddleware_BufferedResponsesIgnoreFlushes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/somepath", nil)
	rec := httptest.NewRecorder()

	middleware := WithResponseBufferMiddleware(1024, 1024, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com", http.StatusFound)

		// Ensure this flush does not bypass the buffered response
		w.(http.Flusher).Flush()

		assert.False(t, rec.Flushed)
	}))

	middleware.ServeHTTP(rec, req)

	// Ensure the buffered response is what we received
	assert.Equal(t, http.StatusFound, rec.Result().StatusCode)
	assert.Contains(t, rec.Body.String(), "http://example.com")
}

func TestResponseBufferMiddleware_SSEResponsesBypassBufferAndAreFlushable(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/somepath", nil)
	rec := httptest.NewRecorder()

	middleware := WithResponseBufferMiddleware(1024, 1024, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		w.Write([]byte("data: hello\n\n"))
		w.(http.Flusher).Flush()

		assert.True(t, rec.Flushed)
	}))

	middleware.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Result().StatusCode)
	assert.Contains(t, rec.Body.String(), "data: hello")
}
