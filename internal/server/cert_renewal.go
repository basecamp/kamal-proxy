package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/idna"
)

var (
	ErrorCertRenewalTLSNotEnabled     = errors.New("TLS is not enabled for this service")
	ErrorCertRenewalStaticCertificate = errors.New("service uses a custom TLS certificate, nothing to renew")
	ErrorCertRenewalNotRootPath       = errors.New("TLS for this service is managed by the service on the root path")
	ErrorCertRenewalHostRequired      = errors.New("a host must be specified for services using a TLS on-demand URL")
	ErrorCertRenewalInvalidHost       = errors.New("invalid host")
	ErrorCertRenewalUnknownHost       = errors.New("host is not served by this service")
	ErrorCertRenewalNotStored         = errors.New("certificate was issued but could not be stored")

	errorCertRenewalFinished = errors.New("certificate renewal has finished")
)

// RenewCertificates forces new certificates to be issued for the given hosts,
// using a snapshot of the service's options. Hosts are renewed one at a time,
// stopping at the first failure. It returns the cache keys of the
// certificates that were renewed, even when it also returns an error.
//
// Issuance uses a separate certificate manager whose cache hides the host's
// existing certificates until a new one is stored. The service's own manager
// keeps serving the existing certificates throughout, and the cache on disk
// is only written once a new certificate has been issued, so a failed
// renewal leaves everything as it was. If anything was renewed, the caller
// should reload the service's certificate manager so the new certificates
// are served.
func (s *Service) RenewCertificates(options ServiceOptions, hosts []string) ([]string, error) {
	s.renewalLock.Lock()
	defer s.renewalLock.Unlock()

	cache := autocert.DirCache(options.ScopedCachePath())
	renewed := []string{}

	for _, host := range hosts {
		stored, err := s.renewCertificate(options, cache, host)
		renewed = append(renewed, stored...)

		if err != nil {
			slog.Error("Certificate renewal failed", "service", s.name, "host", host, "error", err)
			err = fmt.Errorf("unable to renew certificate for %s: %w", host, err)
			if len(renewed) > 0 {
				err = fmt.Errorf("%w (renewed: %s)", err, strings.Join(renewed, ", "))
			}
			return renewed, err
		}

		slog.Info("Certificate renewed", "service", s.name, "host", host)
	}

	return renewed, nil
}

// Private

// certificatesToRenew checks that the service serves its own automatic TLS
// certificates, and returns its options along with the normalized hosts to
// renew. It reads them under the router's lock, so that the renewal itself
// can proceed without holding it.
func (r *Router) certificatesToRenew(name string, hosts []string) (*Service, ServiceOptions, []string, error) {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service := r.services.Get(name)
	if service == nil {
		return nil, ServiceOptions{}, nil, ErrorServiceNotFound
	}

	options := service.options

	// Services on other paths share the TLS settings of the service on the
	// root path, which is the one serving the certificates.
	if !service.servesRootPath() {
		if root := r.services.ServiceForHost(options.Hosts[0]); root != nil {
			return nil, ServiceOptions{}, nil, fmt.Errorf("%w: %s", ErrorCertRenewalNotRootPath, root.name)
		}
		return nil, ServiceOptions{}, nil, ErrorCertRenewalTLSNotEnabled
	}

	switch service.certManager.(type) {
	case *autocert.Manager:
	case *StaticCertManager:
		return nil, ServiceOptions{}, nil, ErrorCertRenewalStaticCertificate
	default:
		return nil, ServiceOptions{}, nil, ErrorCertRenewalTLSNotEnabled
	}

	hosts, err := certificateHostsToRenew(options, hosts)
	if err != nil {
		return nil, ServiceOptions{}, nil, err
	}

	// TLS on-demand services accept any host, except those routed to another
	// service.
	if options.TLSOnDemandURL != "" {
		for _, host := range hosts {
			if r.services.ServiceForHost(host) != service {
				return nil, ServiceOptions{}, nil, fmt.Errorf("%w: %s", ErrorCertRenewalUnknownHost, host)
			}
		}
	}

	return service, options, hosts, nil
}

// reloadCertManager replaces the service's certificate manager, and the
// middleware answering its HTTP challenges, so that certificates are loaded
// from the cache again.
func (s *Service) reloadCertManager() error {
	certManager, err := s.createCertManager(s.options)
	if err != nil {
		return err
	}

	s.certManager = certManager
	s.middleware = s.createMiddleware(s.options, s.handler, certManager)

	return nil
}

// certificateHostsToRenew returns the hosts to renew, defaulting to all of the
// service's hosts. They are normalized as autocert does, so that they match
// its cache keys.
func certificateHostsToRenew(options ServiceOptions, hosts []string) ([]string, error) {
	onDemand := options.TLSOnDemandURL != ""

	if len(hosts) == 0 {
		if onDemand || !options.HasConfiguredHosts() {
			return nil, ErrorCertRenewalHostRequired
		}
		hosts = options.Hosts
	}

	configured := []string{}
	if !onDemand {
		for _, host := range options.Hosts {
			if host, err := normalizeCertificateHost(host); err == nil {
				configured = append(configured, host)
			}
		}
	}

	result := []string{}
	for _, host := range hosts {
		normalized, err := normalizeCertificateHost(host)
		if err != nil {
			return nil, err
		}

		if !onDemand && !slices.Contains(configured, normalized) {
			return nil, fmt.Errorf("%w: %s", ErrorCertRenewalUnknownHost, host)
		}

		if !slices.Contains(result, normalized) {
			result = append(result, normalized)
		}
	}

	return result, nil
}

// normalizeCertificateHost converts a host to the form autocert uses for its
// cache keys: lowercase Punycode, without a trailing dot.
func normalizeCertificateHost(host string) (string, error) {
	normalized, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if err != nil || normalized == "" {
		return "", fmt.Errorf("%w: %q", ErrorCertRenewalInvalidHost, host)
	}
	return normalized, nil
}

// renewCertificate renews the host's ECDSA certificate, and its RSA one if
// that has been issued before. It returns the cache keys of the certificates
// that were stored, which may include some even when it fails.
func (s *Service) renewCertificate(options ServiceOptions, cache autocert.Cache, host string) ([]string, error) {
	type renewal struct {
		key   string
		hello *tls.ClientHelloInfo
	}

	renewals := []renewal{{host, ecdsaClientHello(host)}}

	// Only renew the RSA certificate if one has been issued before, as it is
	// only used by clients that don't support ECDSA.
	if _, err := cache.Get(context.Background(), host+"+rsa"); err == nil {
		renewals = append(renewals, renewal{host + "+rsa", rsaClientHello(host)})
	}

	stored := []string{}
	for _, renewal := range renewals {
		if err := s.issueCertificate(options, cache, renewal.key, renewal.hello); err != nil {
			return stored, err
		}
		stored = append(stored, renewal.key)
	}

	return stored, nil
}

func (s *Service) issueCertificate(options ServiceOptions, cache autocert.Cache, key string, hello *tls.ClientHelloInfo) error {
	renewalCache := newRenewalCache(cache, key)
	manager, err := s.createACMEManager(options, renewalCache, cache)
	if err != nil {
		return err
	}

	// autocert starts a renewal timer for each certificate a manager issues,
	// with no way to stop it. Cut this manager off from the CA once it's done,
	// so the timer can't issue more certificates later on; the service's own
	// manager takes care of regular renewals.
	transport := &renewalTransport{}
	defer transport.close()
	manager.Client.HTTPClient = &http.Client{Transport: transport}

	// The CA's validation requests reach the service's own manager, not this
	// one. That works because autocert also stores challenge responses
	// (tls-alpn-01 certificates and http-01 tokens) in the cache, and falls
	// back to the cache when it doesn't hold them in memory. Allow http-01 as
	// well, as the service's manager does.
	manager.HTTPHandler(nil)

	if _, err := manager.GetCertificate(hello); err != nil {
		return err
	}

	return renewalCache.storeError()
}

func ecdsaClientHello(host string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:        host,
		SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
		SupportedCurves:   []tls.CurveID{tls.CurveP256},
		CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
	}
}

func rsaClientHello(host string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:        host,
		SignatureSchemes:  []tls.SignatureScheme{tls.PSSWithSHA256, tls.PKCS1WithSHA256},
		SupportedCurves:   []tls.CurveID{tls.CurveP256},
		CipherSuites:      []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{tls.VersionTLS12},
	}
}

// renewalTransport passes requests to the CA until it is closed, and fails
// them afterwards.
type renewalTransport struct {
	closed atomic.Bool
}

func (t *renewalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.closed.Load() {
		return nil, errorCertRenewalFinished
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (t *renewalTransport) close() {
	t.closed.Store(true)
}

// renewalCache wraps a certificate cache, hiding the entry for one key until a
// new value is stored for it. This makes autocert issue a new certificate for
// that key, without removing the existing one from the cache first.
type renewalCache struct {
	autocert.Cache

	key      string
	lock     sync.Mutex
	stored   bool
	putError error
}

func newRenewalCache(cache autocert.Cache, key string) *renewalCache {
	return &renewalCache{Cache: cache, key: key}
}

func (c *renewalCache) Get(ctx context.Context, key string) ([]byte, error) {
	if key == c.key && !c.isStored() {
		return nil, autocert.ErrCacheMiss
	}
	return c.Cache.Get(ctx, key)
}

func (c *renewalCache) Put(ctx context.Context, key string, data []byte) error {
	err := c.Cache.Put(ctx, key, data)

	if key == c.key {
		c.lock.Lock()
		if err == nil {
			c.stored = true
		} else {
			c.putError = err
		}
		c.lock.Unlock()
	}
	return err
}

func (c *renewalCache) isStored() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.stored
}

// storeError reports why no new value was stored, if none was. autocert
// itself ignores errors storing new certificates.
func (c *renewalCache) storeError() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	switch {
	case c.stored:
		return nil
	case c.putError != nil:
		return fmt.Errorf("%w: %w", ErrorCertRenewalNotStored, c.putError)
	default:
		return ErrorCertRenewalNotStored
	}
}
