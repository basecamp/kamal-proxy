package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_ServeRequest(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_RedirectToHTTPWhenTLSRequired(t *testing.T) {
	service := testCreateService(t, ServiceOptions{TLSHostname: "example.com"}, defaultTargetOptions)

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

func TestService_MarshallingState(t *testing.T) {
	targetOptions := TargetOptions{
		HealthCheckConfig:          HealthCheckConfig{Path: "/health"},
		BufferRequests:             true,
		MaxRequestMemoryBufferSize: 123,
	}

	service := testCreateService(t, defaultServiceOptions, targetOptions)

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(service)
	require.NoError(t, err)

	var service2 Service
	err = json.NewDecoder(&buf).Decode(&service2)
	require.NoError(t, err)

	assert.Equal(t, service.name, service2.name)
	assert.Equal(t, service.active.Target(), service2.active.Target())
	assert.Equal(t, service.active.options, service2.active.options)
}

func testCreateService(t *testing.T, options ServiceOptions, targetOptions TargetOptions) *Service {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, targetOptions)
	require.NoError(t, err)

	service := NewService("test", "", options)
	service.active = target

	return service
}
