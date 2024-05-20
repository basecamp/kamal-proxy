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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, defaultResponseTimeout)
	require.NoError(t, err)

	service := NewService("test", "", defaultServiceOptions)
	service.active = target

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_EnforceMaxRequestBodySize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, defaultResponseTimeout)
	require.NoError(t, err)

	service := NewService("test", "", ServiceOptions{MaxRequestBodySize: 10})
	service.active = target

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, defaultHealthCheckConfig, defaultResponseTimeout)
	require.NoError(t, err)

	service := NewService("test", "", ServiceOptions{TLSHostname: "example.com"})
	service.active = target
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
