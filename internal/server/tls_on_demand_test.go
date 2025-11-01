package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTLSOnDemandChecker_HostPolicy_EmptyURL(t *testing.T) {
	service := &Service{options: ServiceOptions{Hosts: []string{"example.com"}}}
	checker := NewTLSOnDemandChecker(service)

	policy, _ := checker.HostPolicy()

	// Should allow hosts in the whitelist
	err := policy(context.Background(), "example.com")
	assert.NoError(t, err)

	// Should deny hosts not in the whitelist
	err = policy(context.Background(), "other.com")
	assert.Error(t, err)
}

func TestTLSOnDemandChecker_LocalHostPolicy_Success(t *testing.T) {
	// Create a mock service that returns 200 for /allow-host
	service := &Service{
		options: ServiceOptions{TLSOnDemandUrl: "/allow-host"},
	}
	service.middleware = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/allow-host" && r.URL.Query().Get("host") == "test.example.com" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusForbidden)
		}
	})

	checker := NewTLSOnDemandChecker(service)
	policy := checker.LocalHostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.NoError(t, err)
}

func TestTLSOnDemandChecker_LocalHostPolicy_Denied(t *testing.T) {
	// Create a mock service that returns 403 for /allow-host
	service := &Service{
		options: ServiceOptions{TLSOnDemandUrl: "/allow-host"},
	}
	service.middleware = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Access denied"))
	})

	checker := NewTLSOnDemandChecker(service)
	policy := checker.LocalHostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to get a certificate")
	assert.Contains(t, err.Error(), "status: 403")
}

func TestTLSOnDemandChecker_LocalHostPolicy_LargeResponseBody(t *testing.T) {
	// Create a mock service that returns a large response body
	largeBody := string(make([]byte, 500)) // 500 bytes

	service := &Service{
		options: ServiceOptions{TLSOnDemandUrl: "/allow-host"},
	}
	service.middleware = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(largeBody))
	})

	checker := NewTLSOnDemandChecker(service)
	policy := checker.LocalHostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status: 403")

	// Verify the body is truncated to 256 bytes
	assert.Len(t, err.Error(), 256+len("test.example.com is not allowed to get a certificate (status: 403, body: \"")+len("\")"))
}

func TestTLSOnDemandChecker_ExternalHostPolicy_Success(t *testing.T) {
	// Create a test server that returns 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("host") == "test.example.com" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()

	service := &Service{options: ServiceOptions{TLSOnDemandUrl: server.URL}}
	checker := NewTLSOnDemandChecker(service)
	policy := checker.ExternalHostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.NoError(t, err)
}

func TestTLSOnDemandChecker_ExternalHostPolicy_Denied(t *testing.T) {
	// Create a test server that returns 403
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Access denied"))
	}))
	defer server.Close()

	service := &Service{options: ServiceOptions{TLSOnDemandUrl: server.URL}}
	checker := NewTLSOnDemandChecker(service)
	policy := checker.ExternalHostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to get a certificate")
	assert.Contains(t, err.Error(), "status: 403")
}

func TestTLSOnDemandChecker_HostPolicy_LocalPath(t *testing.T) {
	service := &Service{
		options: ServiceOptions{TLSOnDemandUrl: "/allow-host"},
	}
	service.middleware = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	checker := NewTLSOnDemandChecker(service)
	policy, _ := checker.HostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.NoError(t, err)
}

func TestTLSOnDemandChecker_HostPolicy_ExternalURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := &Service{options: ServiceOptions{TLSOnDemandUrl: server.URL}}
	checker := NewTLSOnDemandChecker(service)
	policy, _ := checker.HostPolicy()

	err := policy(context.Background(), "test.example.com")
	assert.NoError(t, err)
}

func TestTLSOnDemandChecker_HostPolicy_InvalidExternalURL(t *testing.T) {
	service := &Service{options: ServiceOptions{TLSOnDemandUrl: "://invalid-url"}}
	checker := NewTLSOnDemandChecker(service)
	_, err := checker.HostPolicy()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing protocol scheme")
}

func TestTLSOnDemandChecker_buildURLOrPath(t *testing.T) {
	service := &Service{options: ServiceOptions{TLSOnDemandUrl: "/allow-host"}}
	checker := NewTLSOnDemandChecker(service)

	url := checker.buildURLOrPath("test.example.com")
	assert.Equal(t, "/allow-host?host=test.example.com", url)

	// Test with special characters
	url = checker.buildURLOrPath("test.example.com:8080")
	assert.Equal(t, "/allow-host?host=test.example.com%3A8080", url)
}
