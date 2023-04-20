package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func testBackend(t *testing.T, body string) (*httptest.Server, Host) {
	return testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})
}

func testBackendWithHandler(t *testing.T, handler http.HandlerFunc) (*httptest.Server, Host) {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, _ := url.Parse(server.URL)
	host, _ := NewHost(serverURL.Host)

	return server, host
}
