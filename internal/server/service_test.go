package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestService_ServeRequest(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_EnforceMaxRequestBodySize(t *testing.T) {
	service := testCreateService(t, ServiceOptions{MaxRequestBodySize: 10})

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader("Something longer than 10!"))
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
}

func TestService_RedirectToHTTPWhenTLSRequired(t *testing.T) {
	service := testCreateService(t, ServiceOptions{TLSHostname: "example.com"})

	require.True(t, service.options.RequireTLS())

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func testCreateService(t *testing.T, options ServiceOptions) *Service {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultTargetOptions)
	require.NoError(t, err)

	service := NewService("test", "", options)
	service.active = target

	return service
}
