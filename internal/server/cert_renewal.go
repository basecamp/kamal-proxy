package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"golang.org/x/crypto/acme/autocert"
)

var (
	ErrorCertRenewalTLSNotEnabled     = errors.New("TLS is not enabled for this service")
	ErrorCertRenewalStaticCertificate = errors.New("service uses a custom TLS certificate, nothing to renew")
	ErrorCertRenewalHostRequired      = errors.New("a host must be specified for services using a TLS on-demand URL")
	ErrorCertRenewalUnknownHost       = errors.New("host is not served by this service")
	ErrorCertRenewalNotStored         = errors.New("certificate was issued but could not be stored")
)

// RenewCertificates forces new certificates to be issued for the given hosts,
// or for all of the service's hosts when none are given. Hosts are renewed one
// at a time, stopping at the first failure. It returns the hosts that were
// renewed.
//
// Issuance uses a separate certificate manager whose cache hides the host's
// existing certificates until a new one is stored. The service's own manager
// keeps serving the existing certificates throughout, and the cache on disk
// is only written once a new certificate has been issued, so a failed
// renewal leaves everything as it was. Once renewed, the caller should reload
// the service's certificate manager so the new certificates are served.
func (s *Service) RenewCertificates(hosts []string) ([]string, error) {
	s.renewalLock.Lock()
	defer s.renewalLock.Unlock()

	options := s.options

	hosts, err := s.certificateHostsToRenew(options, hosts)
	if err != nil {
		return nil, err
	}

	cache := autocert.DirCache(options.ScopedCachePath())
	renewed := []string{}

	for _, host := range hosts {
		if err := s.renewCertificate(options, cache, host); err != nil {
			slog.Error("Certificate renewal failed", "service", s.name, "host", host, "error", err)
			return renewed, fmt.Errorf("unable to renew certificate for %s: %w", host, err)
		}

		slog.Info("Certificate renewed", "service", s.name, "host", host)
		renewed = append(renewed, host)
	}

	return renewed, nil
}

func (s *Service) ReloadCertManager() error {
	return s.initialize(s.options, s.targetOptions)
}

// Private

func (s *Service) certificateHostsToRenew(options ServiceOptions, hosts []string) ([]string, error) {
	if !options.TLSEnabled {
		return nil, ErrorCertRenewalTLSNotEnabled
	}

	if options.TLSCertificatePath != "" && options.TLSPrivateKeyPath != "" {
		return nil, ErrorCertRenewalStaticCertificate
	}

	onDemand := options.TLSOnDemandURL != ""

	if len(hosts) == 0 {
		if onDemand || !options.HasConfiguredHosts() {
			return nil, ErrorCertRenewalHostRequired
		}
		return options.Hosts, nil
	}

	result := []string{}
	for _, host := range hosts {
		host = strings.TrimSuffix(strings.TrimSpace(host), ".")

		if !onDemand {
			index := slices.IndexFunc(options.Hosts, func(h string) bool { return strings.EqualFold(h, host) })
			if index < 0 {
				return nil, fmt.Errorf("%w: %s", ErrorCertRenewalUnknownHost, host)
			}
			host = options.Hosts[index]
		}

		if !slices.Contains(result, host) {
			result = append(result, host)
		}
	}

	return result, nil
}

func (s *Service) renewCertificate(options ServiceOptions, cache autocert.Cache, host string) error {
	ctx := context.Background()

	hellos := map[string]*tls.ClientHelloInfo{
		host: ecdsaClientHello(host),
	}

	// Only renew the RSA certificate if one has been issued before, as it is
	// only used by clients that don't support ECDSA.
	if _, err := cache.Get(ctx, host+"+rsa"); err == nil {
		hellos[host+"+rsa"] = rsaClientHello(host)
	}

	for _, key := range []string{host, host + "+rsa"} {
		hello, ok := hellos[key]
		if !ok {
			continue
		}

		renewalCache := newRenewalCache(cache, key)
		manager, err := s.createACMEManager(options, renewalCache, cache)
		if err != nil {
			return err
		}
		// The CA's validation requests reach the service's own manager, not
		// this one. That works because autocert also stores challenge
		// responses (tls-alpn-01 certificates and http-01 tokens) in the
		// cache, and falls back to the cache when it doesn't hold them in
		// memory. Allow http-01 as well, as the service's manager does.
		manager.HTTPHandler(nil)

		if _, err := manager.GetCertificate(hello); err != nil {
			return err
		}

		if !renewalCache.stored() {
			return ErrorCertRenewalNotStored
		}
	}

	return nil
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

// renewalCache wraps a certificate cache, hiding the entry for one key until a
// new value is stored for it. This makes autocert issue a new certificate for
// that key, without removing the existing one from the cache first.
type renewalCache struct {
	autocert.Cache

	key    string
	lock   sync.Mutex
	masked bool
}

func newRenewalCache(cache autocert.Cache, key string) *renewalCache {
	return &renewalCache{Cache: cache, key: key, masked: true}
}

func (c *renewalCache) Get(ctx context.Context, key string) ([]byte, error) {
	if key == c.key && c.isMasked() {
		return nil, autocert.ErrCacheMiss
	}
	return c.Cache.Get(ctx, key)
}

func (c *renewalCache) Put(ctx context.Context, key string, data []byte) error {
	if err := c.Cache.Put(ctx, key, data); err != nil {
		return err
	}

	if key == c.key {
		c.lock.Lock()
		c.masked = false
		c.lock.Unlock()
	}
	return nil
}

func (c *renewalCache) Delete(ctx context.Context, key string) error {
	// The existing entry is hidden rather than removed, and must stay in place
	// until it has been replaced.
	if key == c.key && c.isMasked() {
		return nil
	}
	return c.Cache.Delete(ctx, key)
}

func (c *renewalCache) stored() bool {
	return !c.isMasked()
}

func (c *renewalCache) isMasked() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.masked
}
