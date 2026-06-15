package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestService_RedirectToHTTPSWhenTLSRequired(t *testing.T) {
	service := testCreateService(t, ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, TLSRedirect: true}, defaultTargetOptions)

	require.True(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)
	require.Equal(t, "https://example.com/", w.Result().Header.Get("Location"))

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_RedirectDoesNotReflectUnconfiguredHost(t *testing.T) {
	// A request can reach a service via a catch-all binding carrying an
	// arbitrary Host header. The HTTPS redirect must not echo that host into
	// the Location header, or it becomes an open redirect (CWE-601). The
	// redirect target must be pinned to a host we are configured to serve.
	service := testCreateService(t, ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, TLSRedirect: true}, defaultTargetOptions)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/path?q=1", nil)
	req.Host = "evil.example.net"
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)
	require.Equal(t, "https://example.com/path?q=1", w.Result().Header.Get("Location"))
}

func TestService_RedirectPrefersCanonicalHostOverSpoofedHost(t *testing.T) {
	// With a canonical host configured, a spoofed Host must still resolve to the
	// canonical host rather than being reflected.
	service := testCreateService(t, ServiceOptions{Hosts: []string{"example.com"}, CanonicalHost: "www.example.com", TLSEnabled: true, TLSRedirect: true}, defaultTargetOptions)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	req.Host = "evil.example.net"
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)
	require.Equal(t, "https://www.example.com/path", w.Result().Header.Get("Location"))
}

func TestService_HostMatchesPattern(t *testing.T) {
	cases := []struct {
		host, pattern string
		want          bool
	}{
		{"example.com", "example.com", true},
		{"EXAMPLE.com", "example.com", true},        // case-insensitive
		{"app.example.com", "*.example.com", true},  // single-label wildcard
		{"example.com", "*.example.com", false},     // apex doesn't match wildcard
		{"a.b.example.com", "*.example.com", false}, // multi-label (mirrors bindingsForHost)
		{"evil.net", "example.com", false},
		{"example.com", "*.other.com", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, hostMatchesPattern(c.host, c.pattern), "hostMatchesPattern(%q, %q)", c.host, c.pattern)
	}
}

func TestService_SafeRedirectHost(t *testing.T) {
	cases := []struct {
		name    string
		options ServiceOptions
		host    string
		want    string
	}{
		{"configured host kept", ServiceOptions{Hosts: []string{"example.com"}}, "example.com", "example.com"},
		{"unconfigured host pinned to configured", ServiceOptions{Hosts: []string{"example.com"}}, "evil.example.net", "example.com"},
		{"wildcard preserves concrete subdomain", ServiceOptions{Hosts: []string{"*.example.com"}}, "app.example.com", "app.example.com"},
		{"wildcard-only, non-matching host skips", ServiceOptions{Hosts: []string{"*.example.com"}}, "evil.net", ""},
		{"canonical preferred over spoof", ServiceOptions{Hosts: []string{"example.com"}, CanonicalHost: "www.example.com"}, "evil.net", "www.example.com"},
		{"catch-all only skips (no reflect)", ServiceOptions{Hosts: []string{""}}, "evil.net", ""},
		{"no hosts skips", ServiceOptions{}, "evil.net", ""},
		{"wildcard canonical never returned as literal", ServiceOptions{Hosts: []string{"example.com"}, CanonicalHost: "*.example.com"}, "evil.net", "example.com"},
	}
	for _, c := range cases {
		service := &Service{options: c.options}
		require.Equal(t, c.want, service.safeRedirectHost(c.host), c.name)
	}
}

func TestService_RedirectURLSkippedWhenNoSafeHost(t *testing.T) {
	// A catch-all service (no concrete configured host) with TLS redirect must
	// not emit a malformed https:/// Location built from the client Host — it
	// skips the redirect instead.
	service := &Service{options: ServiceOptions{Hosts: []string{""}, TLSEnabled: true, TLSRedirect: true}}
	req := httptest.NewRequest(http.MethodGet, "http://evil.example.net/path", nil)
	require.Equal(t, "", service.redirectURLIfNeeded(req))
}

func TestService_RedirectIgnoresWildcardCanonicalHost(t *testing.T) {
	// A wildcard is a match rule, not a redirect target: a (mis)configured
	// wildcard CanonicalHost must never appear in the Location.
	service := &Service{options: ServiceOptions{Hosts: []string{"example.com"}, CanonicalHost: "*.example.com", TLSEnabled: true, TLSRedirect: true}}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/p", nil)
	require.Equal(t, "https://example.com/p", service.redirectURLIfNeeded(req))
}

func TestService_DontRedirectToHTTPSWhenTLSAndPlainHTTPAllowed(t *testing.T) {
	var forwardedProto string

	service := testCreateServiceWithHandler(t, ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, TLSRedirect: false}, defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwardedProto = r.Header.Get("X-Forwarded-Proto")
		}),
	)

	require.True(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Equal(t, "http", forwardedProto)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Equal(t, "https", forwardedProto)
}

func TestService_UseStaticTLSCertificateWhenConfigured(t *testing.T) {
	certPath, keyPath := prepareTestCertificateFiles(t)

	service := testCreateService(
		t,
		ServiceOptions{
			Hosts:              []string{"example.com"},
			TLSEnabled:         true,
			TLSCertificatePath: certPath,
			TLSPrivateKeyPath:  keyPath,
		},
		defaultTargetOptions,
	)

	require.IsType(t, &StaticCertManager{}, service.certManager)
}

func TestService_RejectTLSRequestsWhenNotConfigured(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	require.False(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Result().StatusCode)
}

func TestService_ReturnSuccessfulHealthCheckWhilePausedOrStopped(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	checkRequest := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusOK, checkRequest("/other"))

	service.Pause(time.Second, time.Millisecond)
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusGatewayTimeout, checkRequest("/other"))

	service.Stop(time.Second, DefaultStopMessage)
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusServiceUnavailable, checkRequest("/other"))

	service.Resume()
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusOK, checkRequest("/other"))
}

func TestService_MarshallingState(t *testing.T) {
	targetOptions := TargetOptions{
		HealthCheckConfig:   HealthCheckConfig{Path: "/health", Interval: time.Second, Timeout: 2 * time.Second},
		BufferRequests:      true,
		MaxMemoryBufferSize: 123,
	}

	service := testCreateService(t, defaultServiceOptions, targetOptions)
	t.Cleanup(service.Dispose)
	require.NoError(t, service.Stop(time.Second, DefaultStopMessage))
	service.UpdateLoadBalancer(NewLoadBalancer(service.active.Targets(), DefaultWriterAffinityTimeout, false), TargetSlotRollout)

	require.NoError(t, service.SetRolloutSplit(20, []string{"first"}))

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(service)
	require.NoError(t, err)

	var service2 Service
	err = json.NewDecoder(&buf).Decode(&service2)
	require.NoError(t, err)
	t.Cleanup(service2.Dispose)

	assert.Equal(t, service.name, service2.name)
	assert.Equal(t, service.active.Targets().Names(), service2.active.Targets().Names())
	assert.Equal(t, service.targetOptions, service2.targetOptions)

	assert.Equal(t, PauseStateStopped, service2.pauseController.GetState())
	assert.Equal(t, DefaultStopMessage, service2.pauseController.GetStopMessage())

	assert.Equal(t, 20, service2.rolloutController.Percentage)
	assert.Equal(t, []string{"first"}, service2.rolloutController.Allowlist)
}

func TestService_UnmarshallingStateFromLegacyFormat(t *testing.T) {
	state := `
	  {
		"name": "my-app",
		"hosts": ["app.example.com"],
		"active_target": "localhost:3000",
		"rollout_target": "",
		"options": {
		  "tls_enabled": false,
		  "tls_certificate_path": "",
		  "tls_private_key_path": "",
		  "acme_directory": "",
		  "acme_cache_path": "",
		  "error_page_path": ""
		},
		"target_options": {
		  "health_check_config": {
			"path": "/up",
			"interval": 1000000000,
			"timeout": 5000000000
		  },
		  "response_timeout": 3000000000,
		  "buffer_requests": false,
		  "buffer_responses": false,
		  "max_memory_buffer_size": 1048576,
		  "max_request_body_size": 0,
		  "max_response_body_size": 0,
		  "log_request_headers": null,
		  "log_response_headers": null,
		  "forward_headers": true
		},
		"pause_controller": {
		  "state": 0,
		  "stop_message": "",
		  "fail_after": 0
		},
		"rollout_controller": null
	  }
	`

	var service Service
	err := json.NewDecoder(strings.NewReader(state)).Decode(&service)
	require.NoError(t, err)
	t.Cleanup(service.Dispose)

	assert.Equal(t, "my-app", service.name)
	assert.Equal(t, []string{"localhost:3000"}, service.active.Targets().Names())
	assert.Equal(t, []string{"app.example.com"}, service.options.Hosts)
	assert.Equal(t, []string{"/"}, service.options.PathPrefixes)
	assert.Equal(t, 3*time.Second, service.targetOptions.ResponseTimeout)
}

func testCreateService(t *testing.T, options ServiceOptions, targetOptions TargetOptions) *Service {
	return testCreateServiceWithHandler(t, options, targetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
}

func testCreateServiceWithHandler(t *testing.T, options ServiceOptions, targetOptions TargetOptions, handler http.Handler) *Service {
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	target, err := NewTarget(serverURL.Host, targetOptions)
	require.NoError(t, err)

	service, err := NewService("test", options, targetOptions)
	require.NoError(t, err)

	service.UpdateLoadBalancer(NewLoadBalancer(TargetList{target}, DefaultWriterAffinityTimeout, false), TargetSlotActive)
	service.active.WaitUntilHealthy(time.Second)

	return service
}
