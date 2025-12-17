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

func TestResponseBufferMiddleware_StreamingResponsesBypassBuffer(t *testing.T) {
	testCases := []struct {
		name         string
		setupHeaders func(http.Header)
		shouldBypass bool
		description  string
	}{
		{
			name: "text/event-stream bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/event-stream")
			},
			shouldBypass: true,
			description:  "Server-Sent Events should bypass buffering",
		},
		{
			name: "text/event-stream with charset bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/event-stream; charset=utf-8")
			},
			shouldBypass: true,
			description:  "Server-Sent Events with charset should bypass buffering",
		},
		{
			name: "chunked transfer encoding bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Content-Type", "text/plain")
			},
			shouldBypass: true,
			description:  "Chunked encoding indicates streaming and should bypass buffering",
		},
		{
			name: "chunked with text/event-stream bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Content-Type", "text/event-stream")
			},
			shouldBypass: true,
			description:  "Both chunked and SSE should bypass buffering",
		},
		{
			name: "MessageBus long-polling with chunked bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Content-Type", "text/plain")
			},
			shouldBypass: true,
			description:  "MessageBus-style long polling with chunked encoding should bypass buffering",
		},
		{
			name: "streaming JSON with chunked bypasses buffer",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Content-Type", "application/json")
			},
			shouldBypass: true,
			description:  "Streaming JSON responses with chunked encoding should bypass buffering",
		},
		{
			name: "regular response is buffered",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/plain")
				h.Set("Content-Length", "100")
			},
			shouldBypass: false,
			description:  "Regular responses with Content-Length should be buffered",
		},
		{
			name: "regular JSON response is buffered",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "application/json")
				h.Set("Content-Length", "50")
			},
			shouldBypass: false,
			description:  "Regular JSON responses with Content-Length should be buffered",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example.com/somepath", nil)
			rec := httptest.NewRecorder()

			middleware := WithResponseBufferMiddleware(1024, 1024, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Setup headers as specified in test case
				tc.setupHeaders(w.Header())
				w.WriteHeader(http.StatusOK)

				// Write some data
				w.Write([]byte("test data"))

				// Try to flush - this should only work if bypassing buffer
				w.(http.Flusher).Flush()

				// Check if the response was flushed (indicating bypass)
				assert.Equal(t, tc.shouldBypass, rec.Flushed, tc.description)
			}))

			middleware.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Result().StatusCode)
			assert.Contains(t, rec.Body.String(), "test data")
		})
	}
}

func TestBufferedResponseWriter_ShouldSwitchToUnbuffered(t *testing.T) {
	testCases := []struct {
		name         string
		setupHeaders func(http.Header)
		expected     bool
		description  string
	}{
		{
			name: "text/event-stream should switch",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/event-stream")
			},
			expected:    true,
			description: "Server-Sent Events should switch to unbuffered",
		},
		{
			name: "text/event-stream with charset should switch",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/event-stream; charset=utf-8")
			},
			expected:    true,
			description: "Server-Sent Events with charset should switch to unbuffered",
		},
		{
			name: "chunked transfer encoding should switch",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
			},
			expected:    true,
			description: "Chunked transfer encoding should switch to unbuffered",
		},
		{
			name: "chunked with any content type should switch",
			setupHeaders: func(h http.Header) {
				h.Set("Transfer-Encoding", "chunked")
				h.Set("Content-Type", "application/json")
			},
			expected:    true,
			description: "Chunked encoding should switch regardless of content type",
		},
		{
			name: "regular content should not switch",
			setupHeaders: func(h http.Header) {
				h.Set("Content-Type", "text/plain")
			},
			expected:    false,
			description: "Regular content without streaming indicators should not switch",
		},
		{
			name: "empty headers should not switch",
			setupHeaders: func(h http.Header) {
				// No headers set
			},
			expected:    false,
			description: "Empty headers should not switch to unbuffered",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writer := &bufferedResponseWriter{
				ResponseWriter: rec,
				statusCode:     http.StatusOK,
			}

			// Setup headers as specified in test case
			tc.setupHeaders(writer.Header())

			result := writer.ShouldSwitchToUnbuffered()
			assert.Equal(t, tc.expected, result, tc.description)
		})
	}
}
