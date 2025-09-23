package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CookieScope handles scoping Set-Cookie paths to a path prefix.
type CookieScope struct {
	pathPrefix string
	host       string
}

func NewCookieScope(pathPrefix string, host string) *CookieScope {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	return &CookieScope{
		pathPrefix: pathPrefix,
		host:       host,
	}
}

func (cs *CookieScope) ApplyToHeader(header http.Header) {
	cookies := header["Set-Cookie"]
	for i, v := range cookies {
		cookie, err := http.ParseSetCookie(v)
		if err != nil || !cs.domainMatches(cookie.Domain) {
			continue
		}

		cookie.Path, err = url.JoinPath(cs.pathPrefix, strings.Trim(cookie.Path, "/"))
		if err == nil {
			cookies[i] = cookie.String()
		}
	}
}

func (cs *CookieScope) domainMatches(cookieDomain string) bool {
	return cookieDomain == "" || cookieDomain == cs.host
}
