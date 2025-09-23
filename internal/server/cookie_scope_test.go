package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCookieScope_ApplyToHeader(t *testing.T) {
	tests := []struct {
		name           string
		pathPrefix     string
		host           string
		inputCookies   []string
		expectedPaths  []string
	}{
		{
			name:           "scopes cookie with root path",
			pathPrefix:     "/api",
			host:           "example.com",
			inputCookies:   []string{"session=abc; Path=/"},
			expectedPaths:  []string{"/api"},
		},
		{
			name:           "scopes cookie with subpath",
			pathPrefix:     "/api",
			host:           "example.com",
			inputCookies:   []string{"session=abc; Path=/admin"},
			expectedPaths:  []string{"/api/admin"},
		},
		{
			name:           "scopes cookie without path",
			pathPrefix:     "/api",
			host:           "example.com",
			inputCookies:   []string{"session=abc"},
			expectedPaths:  []string{"/api"},
		},
		{
			name:           "scopes first-party cookie with matching domain",
			pathPrefix:     "/api",
			host:           "example.com",
			inputCookies:   []string{"session=abc; Path=/; Domain=example.com"},
			expectedPaths:  []string{"/api"},
		},
		{
			name:           "does not scope third-party cookie",
			pathPrefix:     "/api",
			host:           "example.com",
			inputCookies:   []string{"tracking=xyz; Path=/; Domain=other.com"},
			expectedPaths:  []string{"/"},
		},
		{
			name:           "handles multiple cookies",
			pathPrefix:     "/app",
			host:           "example.com",
			inputCookies:   []string{"a=1; Path=/", "b=2; Path=/foo", "c=3; Path=/; Domain=third.com"},
			expectedPaths:  []string{"/app", "/app/foo", "/"},
		},
		{
			name:           "handles host with port",
			pathPrefix:     "/api",
			host:           "example.com:8080",
			inputCookies:   []string{"session=abc; Path=/; Domain=example.com"},
			expectedPaths:  []string{"/api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewCookieScope(tt.pathPrefix, tt.host)
			header := http.Header{}
			header["Set-Cookie"] = tt.inputCookies

			cs.ApplyToHeader(header)

			cookies := header["Set-Cookie"]
			assert.Len(t, cookies, len(tt.expectedPaths))

			for i, cookieStr := range cookies {
				cookie, err := http.ParseSetCookie(cookieStr)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPaths[i], cookie.Path, "cookie %d path mismatch", i)
			}
		})
	}
}

func TestCookieScope_DomainMatching(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		cookieDomain string
		shouldScope  bool
	}{
		{
			name:         "empty domain matches",
			host:         "example.com",
			cookieDomain: "",
			shouldScope:  true,
		},
		{
			name:         "exact domain match",
			host:         "example.com",
			cookieDomain: "example.com",
			shouldScope:  true,
		},
		{
			name:         "different domain does not match",
			host:         "example.com",
			cookieDomain: "other.com",
			shouldScope:  false,
		},
		{
			name:         "subdomain does not match parent",
			host:         "example.com",
			cookieDomain: "sub.example.com",
			shouldScope:  false,
		},
		{
			name:         "parent does not match subdomain host",
			host:         "sub.example.com",
			cookieDomain: "example.com",
			shouldScope:  false,
		},
		{
			name:         "host with port matches domain",
			host:         "example.com:8080",
			cookieDomain: "example.com",
			shouldScope:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := NewCookieScope("/api", tt.host)

			header := http.Header{}
			if tt.cookieDomain == "" {
				header["Set-Cookie"] = []string{"test=value; Path=/original"}
			} else {
				header["Set-Cookie"] = []string{"test=value; Path=/original; Domain=" + tt.cookieDomain}
			}

			cs.ApplyToHeader(header)

			cookie, err := http.ParseSetCookie(header["Set-Cookie"][0])
			assert.NoError(t, err)

			if tt.shouldScope {
				assert.Equal(t, "/api/original", cookie.Path)
			} else {
				assert.Equal(t, "/original", cookie.Path)
			}
		})
	}
}
