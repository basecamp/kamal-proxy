package server

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestBufferMiddleware(t *testing.T) {
	sendRequest := func(requestBody, responseBody string) *httptest.ResponseRecorder {
		middleware := WithRequestBufferMiddleware(4, 8, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}

func TestRequestBufferMiddleware_MalformedChunkedEncoding(t *testing.T) {
	middleware := WithRequestBufferMiddleware(1024, 4096, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	server := httptest.NewServer(middleware)
	t.Cleanup(server.Close)

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	rawRequest := "POST / HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"\r\n" +
		"ZZ\r\n"

	_, err = conn.Write([]byte(rawRequest))
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
