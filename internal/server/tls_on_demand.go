package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

const (
	tlsOnDemandCheckTimeout      = 2 * time.Second
	tlsOnDemandCheckMaxBodyBytes = 256
)

// tlsOnDemandChecker builds an autocert.HostPolicy that approves hosts by
// querying the configured TLS on-demand endpoint (with a `host` query
// parameter); a 200 response allows certificate issuance.
type tlsOnDemandChecker struct {
	service   *Service
	certCache autocert.Cache
	endpoint  *url.URL
	local     bool // endpoint is a path served by the service itself

	checkTimeout time.Duration
}

func newTLSOnDemandChecker(service *Service, onDemandURL string, certCache autocert.Cache) (*tlsOnDemandChecker, error) {
	endpoint, local, err := parseTLSOnDemandURL(onDemandURL)
	if err != nil {
		return nil, err
	}

	return &tlsOnDemandChecker{
		service:      service,
		certCache:    certCache,
		endpoint:     endpoint,
		local:        local,
		checkTimeout: tlsOnDemandCheckTimeout,
	}, nil
}

// parseTLSOnDemandURL parses a TLS on-demand URL, returning the parsed URL and
// whether it names a path on the service itself rather than an external
// endpoint.
func parseTLSOnDemandURL(onDemandURL string) (*url.URL, bool, error) {
	if strings.HasPrefix(onDemandURL, "//") {
		return nil, false, fmt.Errorf("tls-on-demand-url must be a path or an absolute http(s) URL")
	}

	if strings.HasPrefix(onDemandURL, "/") {
		endpoint, err := url.Parse(onDemandURL)
		if err != nil {
			return nil, false, fmt.Errorf("unable to parse tls-on-demand-url: %w", err)
		}
		return endpoint, true, nil
	}

	endpoint, err := url.ParseRequestURI(onDemandURL)
	if err != nil {
		return nil, false, fmt.Errorf("unable to parse tls-on-demand-url: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, false, fmt.Errorf("unsupported scheme %q in tls-on-demand-url", endpoint.Scheme)
	}
	if endpoint.Host == "" {
		return nil, false, fmt.Errorf("missing host in tls-on-demand-url")
	}
	return endpoint, false, nil
}

func validateTLSOnDemandURL(onDemandURL string) error {
	_, _, err := parseTLSOnDemandURL(onDemandURL)
	return err
}

func (c *tlsOnDemandChecker) hostPolicy() autocert.HostPolicy {
	policy := c.externalHostPolicy()
	if c.local {
		policy = c.localHostPolicy()
	}

	return func(ctx context.Context, host string) error {
		// Since x/crypto v0.34.0, autocert consults the host policy on every
		// handshake, not just when issuing a certificate. A host that
		// already has a certificate was approved when that certificate was
		// issued, so we skip re-checking it. This keeps the endpoint off the
		// handshake hot path, and means an endpoint outage cannot affect
		// hosts that already serve TLS.
		if c.hasCachedCert(ctx, host) {
			return nil
		}

		return policy(ctx, host)
	}
}

// hasCachedCert reports whether the certificate cache already holds a
// certificate for the host. autocert stores certificates keyed by the
// punycoded domain (which the host already is, by the time the policy is
// called), with a "+rsa" variant for RSA-only clients.
func (c *tlsOnDemandChecker) hasCachedCert(ctx context.Context, host string) bool {
	host = strings.TrimSuffix(host, ".")

	for _, key := range []string{host, host + "+rsa"} {
		if _, err := c.certCache.Get(ctx, key); err == nil {
			return true
		}
	}
	return false
}

// localHostPolicy approves hosts by routing a request for the configured path
// through the service's own handler chain, so the backend application decides
// which hosts are allowed.
func (c *tlsOnDemandChecker) localHostPolicy() autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		ctx, cancel := context.WithTimeout(ctx, c.checkTimeout)
		defer cancel()
		ctx = markInternalRequest(ctx)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.checkURL(host), http.NoBody)
		if err != nil {
			return err
		}

		// The probe carries the hostname being validated, so backends that
		// use host authorization see that rather than the target's address.
		req.Host = host

		recorder := newBoundedResponseRecorder(tlsOnDemandCheckMaxBodyBytes)
		c.service.ServeHTTP(recorder, req)

		if recorder.status != http.StatusOK {
			return c.denialError(host, recorder.status, recorder.body.String())
		}
		return nil
	}
}

// externalHostPolicy approves hosts by making an HTTP request to the
// configured external endpoint.
func (c *tlsOnDemandChecker) externalHostPolicy() autocert.HostPolicy {
	client := &http.Client{
		Timeout: c.checkTimeout,

		// A redirect is the endpoint's final answer; following it could turn
		// a denial into a 200 from wherever it redirects to.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return func(ctx context.Context, host string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.checkURL(host), http.NoBody)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, tlsOnDemandCheckMaxBodyBytes))
			return c.denialError(host, resp.StatusCode, string(body))
		}
		return nil
	}
}

func (c *tlsOnDemandChecker) checkURL(host string) string {
	endpoint := *c.endpoint

	query := endpoint.Query()
	query.Set("host", host)
	endpoint.RawQuery = query.Encode()

	return endpoint.String()
}

func (c *tlsOnDemandChecker) denialError(host string, status int, body string) error {
	slog.Warn("TLS on-demand check denied host", "host", host, "status", status, "body", body)

	return fmt.Errorf("%s is not allowed to get a certificate (status: %d, body: %q)", host, status, body)
}

// boundedResponseRecorder is an http.ResponseWriter that captures the response
// status and at most `limit` bytes of the body, discarding the rest.
type boundedResponseRecorder struct {
	status int
	body   bytes.Buffer
	limit  int
	header http.Header
}

func newBoundedResponseRecorder(limit int) *boundedResponseRecorder {
	return &boundedResponseRecorder{status: http.StatusOK, limit: limit, header: http.Header{}}
}

func (r *boundedResponseRecorder) Header() http.Header {
	return r.header
}

func (r *boundedResponseRecorder) WriteHeader(status int) {
	r.status = status
}

func (r *boundedResponseRecorder) Write(p []byte) (int, error) {
	if remaining := r.limit - r.body.Len(); remaining > 0 {
		r.body.Write(p[:min(len(p), remaining)])
	}
	return len(p), nil
}
