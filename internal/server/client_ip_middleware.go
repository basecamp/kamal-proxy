package server

import (
	"net"
	"net/http"
	"strings"
)

type ClientIPMiddleware struct {
	headerName string
	trusted    bool
	next       http.Handler
}

func WithClientIPMiddleware(clientIPHeader string, forwardHeaders bool, next http.Handler) http.Handler {
	headerName, trusted := "X-Forwarded-For", forwardHeaders
	if clientIPHeader != "" {
		headerName, trusted = clientIPHeader, true
	}

	return &ClientIPMiddleware{
		headerName: http.CanonicalHeaderKey(headerName),
		trusted:    trusted,
		next:       next,
	}
}

// ClientIP returns the address resolved by ClientIPMiddleware, falling back to
// the peer address for requests that did not pass through it.
func ClientIP(r *http.Request) string {
	if clientIP := LoggingRequestContext(r).ClientIP; clientIP != "" {
		return clientIP
	}
	return remoteAddrHost(r.RemoteAddr)
}

func (h *ClientIPMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	forwardedFor := h.trustedForwardedFor(r)
	if len(forwardedFor) == 0 {
		r.Header.Del("X-Forwarded-For")
	} else {
		r.Header["X-Forwarded-For"] = forwardedFor
	}

	LoggingRequestContext(r).ClientIP = clientIPFromForwardedFor(forwardedFor, r.RemoteAddr)

	h.next.ServeHTTP(w, r)
}

func (h *ClientIPMiddleware) trustedForwardedFor(r *http.Request) []string {
	if !h.trusted {
		return nil
	}
	return r.Header[h.headerName]
}

func clientIPFromForwardedFor(forwardedFor []string, remoteAddr string) string {
	if len(forwardedFor) > 0 {
		clientIP, _, _ := strings.Cut(forwardedFor[0], ",")
		if clientIP = strings.TrimSpace(clientIP); clientIP != "" {
			return clientIP
		}
	}
	return remoteAddrHost(remoteAddr)
}

func remoteAddrHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
