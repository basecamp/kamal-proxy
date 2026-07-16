package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme/autocert"
)

func testHostPolicy(t *testing.T, service *Service, onDemandURL string) autocert.HostPolicy {
	checker, err := newTLSOnDemandChecker(service, onDemandURL, autocert.DirCache(t.TempDir()))
	require.NoError(t, err)
	return checker.hostPolicy()
}

func TestTLSOnDemandChecker_LocalHostPolicy(t *testing.T) {
	service := testCreateServiceWithHandler(
		t,
		ServiceOptions{TLSOnDemandURL: "/allow-host"},
		defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/up" {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/allow-host" && r.URL.Query().Get("host") == "allowed.example.com" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Access denied"))
		}),
	)

	policy := testHostPolicy(t, service, service.options.TLSOnDemandURL)

	assert.NoError(t, policy(context.Background(), "allowed.example.com"))

	err := policy(context.Background(), "denied.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to get a certificate")
	assert.Contains(t, err.Error(), "status: 403")
	assert.Contains(t, err.Error(), "Access denied")
}

func TestTLSOnDemandChecker_LocalHostPolicy_IsNotRedirectedWhenTLSRedirectEnabled(t *testing.T) {
	var forwardedProto string

	service := testCreateServiceWithHandler(
		t,
		ServiceOptions{
			TLSEnabled:     true,
			TLSRedirect:    true,
			TLSOnDemandURL: "/allow-host",
		},
		defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/up" {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/allow-host" && r.URL.Query().Get("host") == "allowed.example.com" {
				forwardedProto = r.Header.Get("X-Forwarded-Proto")
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusForbidden)
		}),
	)

	policy := testHostPolicy(t, service, service.options.TLSOnDemandURL)

	assert.NoError(t, policy(context.Background(), "allowed.example.com"))
	assert.Equal(t, "http", forwardedProto)
}

func TestTLSOnDemandChecker_LocalHostPolicy_SetsHostHeaderToCheckedHost(t *testing.T) {
	var checkHost string

	service := testCreateServiceWithHandler(
		t,
		ServiceOptions{TLSOnDemandURL: "/allow-host"},
		defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/allow-host" {
				checkHost = r.Host
			}
			w.WriteHeader(http.StatusOK)
		}),
	)

	policy := testHostPolicy(t, service, service.options.TLSOnDemandURL)

	assert.NoError(t, policy(context.Background(), "allowed.example.com"))
	assert.Equal(t, "allowed.example.com", checkHost)
}

func TestTLSOnDemandChecker_LocalHostPolicy_TruncatesLargeResponseBodies(t *testing.T) {
	service := testCreateServiceWithHandler(
		t,
		ServiceOptions{TLSOnDemandURL: "/allow-host"},
		defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/up" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(strings.Repeat("a", 500)))
		}),
	)

	policy := testHostPolicy(t, service, service.options.TLSOnDemandURL)

	err := policy(context.Background(), "denied.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), strings.Repeat("a", 256))
	assert.NotContains(t, err.Error(), strings.Repeat("a", 257))
}

func TestTLSOnDemandChecker_LocalHostPolicy_DeniesWhenStopped(t *testing.T) {
	service := testCreateServiceWithHandler(
		t,
		ServiceOptions{TLSOnDemandURL: "/allow-host"},
		defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	policy := testHostPolicy(t, service, service.options.TLSOnDemandURL)

	require.NoError(t, service.Stop(time.Second, "stopped for maintenance"))

	err := policy(context.Background(), "allowed.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to get a certificate")
}

func TestTLSOnDemandChecker_ExternalHostPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("host") == "allowed.example.com" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Access denied"))
	}))
	defer server.Close()

	service := &Service{}
	policy := testHostPolicy(t, service, server.URL)

	assert.NoError(t, policy(context.Background(), "allowed.example.com"))

	err := policy(context.Background(), "denied.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to get a certificate")
	assert.Contains(t, err.Error(), "status: 403")
	assert.Contains(t, err.Error(), "Access denied")
}

func TestTLSOnDemandChecker_ExternalHostPolicy_DoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/allowed" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/allowed", http.StatusFound)
	}))
	defer server.Close()

	service := &Service{}
	policy := testHostPolicy(t, service, server.URL)

	err := policy(context.Background(), "denied.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status: 302")
}

func TestTLSOnDemandChecker_ExternalHostPolicy_HonoursContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := &Service{}
	policy := testHostPolicy(t, service, server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, policy(ctx, "allowed.example.com"))
}

func TestTLSOnDemandChecker_SkipsCheckWhenHostAlreadyHasCert(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	// Certificates live in the cache keyed by domain, with a "+rsa" variant
	// for RSA-only clients.
	certCache := autocert.DirCache(t.TempDir())
	require.NoError(t, certCache.Put(context.Background(), "cached.example.com", []byte("cert")))
	require.NoError(t, certCache.Put(context.Background(), "rsa-only.example.com+rsa", []byte("cert")))

	checker, err := newTLSOnDemandChecker(&Service{}, server.URL, certCache)
	require.NoError(t, err)
	policy := checker.hostPolicy()

	assert.NoError(t, policy(context.Background(), "cached.example.com"))
	assert.NoError(t, policy(context.Background(), "cached.example.com."))
	assert.NoError(t, policy(context.Background(), "rsa-only.example.com"))
	assert.Equal(t, 0, requests)

	assert.Error(t, policy(context.Background(), "uncached.example.com"))
	assert.Equal(t, 1, requests)
}

func TestTLSOnDemandChecker_CheckURL(t *testing.T) {
	checkURL := func(onDemandURL, host string) string {
		checker, err := newTLSOnDemandChecker(&Service{}, onDemandURL, autocert.DirCache(t.TempDir()))
		require.NoError(t, err)
		return checker.checkURL(host)
	}

	assert.Equal(t, "/allow-host?host=test.example.com", checkURL("/allow-host", "test.example.com"))
	assert.Equal(t, "/allow-host?host=test.example.com%3A8080", checkURL("/allow-host", "test.example.com:8080"))
	assert.Equal(t, "/allow-host?host=test.example.com&token=abc", checkURL("/allow-host?token=abc", "test.example.com"))
	assert.Equal(t, "https://example.com/check?host=test.example.com&token=abc", checkURL("https://example.com/check?token=abc", "test.example.com"))
}

func TestValidateTLSOnDemandURL(t *testing.T) {
	assert.NoError(t, validateTLSOnDemandURL("/allow-host"))
	assert.NoError(t, validateTLSOnDemandURL("/allow-host?token=abc"))
	assert.NoError(t, validateTLSOnDemandURL("http://example.com/check"))
	assert.NoError(t, validateTLSOnDemandURL("https://example.com/check"))

	assert.ErrorContains(t, validateTLSOnDemandURL("://invalid-url"), "unable to parse tls-on-demand-url")
	assert.ErrorContains(t, validateTLSOnDemandURL("ftp://example.com/check"), "unsupported scheme")
	assert.ErrorContains(t, validateTLSOnDemandURL("allow-host"), "unable to parse tls-on-demand-url")
	assert.ErrorContains(t, validateTLSOnDemandURL("http://"), "missing host")
	assert.ErrorContains(t, validateTLSOnDemandURL("//example.com/check"), "must be a path or an absolute http(s) URL")
}
