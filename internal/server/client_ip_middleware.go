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
	if clientIP := RequestContext(r).ClientIP; clientIP != "" {
		return clientIP
	}
	return remoteAddrHost(r.RemoteAddr)
}

func (h *ClientIPMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	forwardedFor := h.trustedForwardedFor(r)
	clientIP := clientIPFromForwardedFor(forwardedFor)

	if clientIP == "" {
		r.Header.Del("X-Forwarded-For")
		clientIP = remoteAddrHost(r.RemoteAddr)
	} else {
		r.Header.Set("X-Forwarded-For", forwardedFor)
	}

	RequestContext(r).ClientIP = clientIP

	h.next.ServeHTTP(w, r)
}

func (h *ClientIPMiddleware) trustedForwardedFor(r *http.Request) string {
	if !h.trusted {
		return ""
	}
	return strings.Join(r.Header[h.headerName], ", ")
}

func clientIPFromForwardedFor(forwardedFor string) string {
	clientIP, _, _ := strings.Cut(forwardedFor, ",")
	return strings.TrimSpace(clientIP)
}

func remoteAddrHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
