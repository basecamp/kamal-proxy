package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMiddleware_ClientIPResolution(t *testing.T) {
	tests := []struct {
		name             string
		clientIPHeader   string
		forwardHeaders   bool
		requestHeaders   map[string]string
		extraHeaders     map[string]string
		wantForwardedFor []string
		wantClientIP     string
	}{
		{
			name:             "forwarded X-Forwarded-For is trusted",
			forwardHeaders:   true,
			requestHeaders:   map[string]string{"X-Forwarded-For": "203.0.113.9"},
			wantForwardedFor: []string{"203.0.113.9"},
			wantClientIP:     "203.0.113.9",
		},
		{
			name:             "leftmost entry is the client when a list of proxies is trusted",
			forwardHeaders:   true,
			requestHeaders:   map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.1"},
			wantForwardedFor: []string{"203.0.113.9, 10.0.0.1"},
			wantClientIP:     "203.0.113.9",
		},
		{
			name:             "X-Forwarded-For is removed when we aren't forwarding headers",
			forwardHeaders:   false,
			requestHeaders:   map[string]string{"X-Forwarded-For": "6.6.6.6"},
			wantForwardedFor: nil,
			wantClientIP:     "192.0.2.1",
		},
		{
			name:           "client IP header is trusted without forwarded headers, and replaces X-Forwarded-For",
			clientIPHeader: "True-Client-IP",
			forwardHeaders: false,
			requestHeaders: map[string]string{
				"X-Forwarded-For": "6.6.6.6",
				"True-Client-IP":  "203.0.113.9",
			},
			wantForwardedFor: []string{"203.0.113.9"},
			wantClientIP:     "203.0.113.9",
		},
		{
			name:           "client IP header replaces X-Forwarded-For even when it's forwarded",
			clientIPHeader: "True-Client-IP",
			forwardHeaders: true,
			requestHeaders: map[string]string{
				"X-Forwarded-For": "6.6.6.6",
				"True-Client-IP":  "203.0.113.9",
			},
			wantForwardedFor: []string{"203.0.113.9"},
			wantClientIP:     "203.0.113.9",
		},
		{
			name:             "absent client IP header removes X-Forwarded-For",
			clientIPHeader:   "True-Client-IP",
			forwardHeaders:   true,
			requestHeaders:   map[string]string{"X-Forwarded-For": "6.6.6.6"},
			wantForwardedFor: nil,
			wantClientIP:     "192.0.2.1",
		},
		{
			name:             "empty client IP header is treated as absent",
			clientIPHeader:   "True-Client-IP",
			requestHeaders:   map[string]string{"True-Client-IP": " "},
			wantForwardedFor: nil,
			wantClientIP:     "192.0.2.1",
		},
		{
			name:             "repeated trusted header values are joined into one entry",
			clientIPHeader:   "True-Client-IP",
			requestHeaders:   map[string]string{"True-Client-IP": "203.0.113.9"},
			extraHeaders:     map[string]string{"True-Client-IP": "10.0.0.1"},
			wantForwardedFor: []string{"203.0.113.9, 10.0.0.1"},
			wantClientIP:     "203.0.113.9",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var forwardedFor []string
			var clientIP string

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwardedFor = r.Header["X-Forwarded-For"]
				clientIP = ClientIP(r)
			})

			middleware := WithRequestContextMiddleware(
				WithClientIPMiddleware(tt.clientIPHeader, tt.forwardHeaders, handler))

			req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
			for name, value := range tt.requestHeaders {
				req.Header.Set(name, value)
			}
			for name, value := range tt.extraHeaders {
				req.Header.Add(name, value)
			}

			middleware.ServeHTTP(httptest.NewRecorder(), req)

			assert.Equal(t, tt.wantForwardedFor, forwardedFor)
			assert.Equal(t, tt.wantClientIP, clientIP)
		})
	}
}

func TestMiddleware_ClientIPWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/", nil)
	req.Header.Set("X-Forwarded-For", "6.6.6.6")

	assert.Equal(t, "192.0.2.1", ClientIP(req))
}
