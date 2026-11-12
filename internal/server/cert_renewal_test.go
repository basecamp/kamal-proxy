package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme/autocert"
)

func TestRouter_RenewCertificates_UnknownService(t *testing.T) {
	router := testRouter(t)

	err := router.RenewCertificates("missing", nil)
	require.ErrorIs(t, err, ErrorServiceNotFound)
}

func TestRouter_RenewCertificates_TLSNotEnabled(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	err := router.RenewCertificates("service1", nil)
	require.ErrorIs(t, err, ErrorCertRenewalTLSNotEnabled)
}

func TestRouter_RenewCertificates_StaticCertificate(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)
	certPath, keyPath := prepareTestCertificateFiles(t)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	err := router.RenewCertificates("service1", nil)
	require.ErrorIs(t, err, ErrorCertRenewalStaticCertificate)
	require.ErrorContains(t, err, "nothing to renew")
}

func TestRouter_RenewCertificates_UnknownHost(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.ACMECachePath = t.TempDir()
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	err := router.RenewCertificates("service1", []string{"other.example.com"})
	require.ErrorIs(t, err, ErrorCertRenewalUnknownHost)
	require.ErrorContains(t, err, "other.example.com")
}

func TestRouter_RenewCertificates_FailureKeepsExistingCertificates(t *testing.T) {
	acmeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"type":"urn:ietf:params:acme:error:malformed","detail":"directory unavailable"}`))
	}))
	defer acmeServer.Close()

	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com", "www.example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.ACMEDirectory = acmeServer.URL
	serviceOptions.ACMECachePath = t.TempDir()
	require.NoError(t, router.DeployService("service1", []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	ctx := context.Background()
	cache := autocert.DirCache(serviceOptions.ScopedCachePath())
	require.NoError(t, cache.Put(ctx, "example.com", []byte("existing ecdsa certificate")))
	require.NoError(t, cache.Put(ctx, "example.com+rsa", []byte("existing rsa certificate")))

	service := router.serviceForName("service1")
	certManager := service.certManager

	err := router.RenewCertificates("service1", nil)
	require.ErrorContains(t, err, "unable to renew certificate for example.com")
	require.ErrorContains(t, err, "directory unavailable")

	data, err := cache.Get(ctx, "example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing ecdsa certificate", string(data))

	data, err = cache.Get(ctx, "example.com+rsa")
	require.NoError(t, err)
	assert.Equal(t, "existing rsa certificate", string(data))

	assert.Same(t, certManager, service.certManager, "the certificate manager is not replaced when renewal fails")
}

func TestService_CertificateHostsToRenew(t *testing.T) {
	service := &Service{name: "test"}

	options := ServiceOptions{TLSEnabled: true, Hosts: []string{"example.com", "www.example.com"}}

	t.Run("defaults to the service's hosts", func(t *testing.T) {
		hosts, err := service.certificateHostsToRenew(options, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"example.com", "www.example.com"}, hosts)
	})

	t.Run("accepts a subset of the service's hosts", func(t *testing.T) {
		hosts, err := service.certificateHostsToRenew(options, []string{"WWW.example.com.", "www.example.com"})
		require.NoError(t, err)
		assert.Equal(t, []string{"www.example.com"}, hosts)
	})

	t.Run("rejects hosts the service doesn't serve", func(t *testing.T) {
		_, err := service.certificateHostsToRenew(options, []string{"example.com", "other.example.com"})
		require.ErrorIs(t, err, ErrorCertRenewalUnknownHost)
	})

	t.Run("rejects services without TLS", func(t *testing.T) {
		_, err := service.certificateHostsToRenew(ServiceOptions{Hosts: []string{"example.com"}}, nil)
		require.ErrorIs(t, err, ErrorCertRenewalTLSNotEnabled)
	})

	t.Run("rejects services with a custom certificate", func(t *testing.T) {
		staticOptions := options
		staticOptions.TLSCertificatePath = "cert.pem"
		staticOptions.TLSPrivateKeyPath = "key.pem"

		_, err := service.certificateHostsToRenew(staticOptions, nil)
		require.ErrorIs(t, err, ErrorCertRenewalStaticCertificate)
	})

	t.Run("requires a host for TLS on-demand services", func(t *testing.T) {
		onDemandOptions := ServiceOptions{TLSEnabled: true, TLSOnDemandURL: "/allow", Hosts: []string{""}}

		_, err := service.certificateHostsToRenew(onDemandOptions, nil)
		require.ErrorIs(t, err, ErrorCertRenewalHostRequired)

		hosts, err := service.certificateHostsToRenew(onDemandOptions, []string{"customer.example.com"})
		require.NoError(t, err)
		assert.Equal(t, []string{"customer.example.com"}, hosts)
	})
}

func TestRenewalCache(t *testing.T) {
	ctx := context.Background()

	stub := newStubCache()
	stub.entries["example.com"] = []byte("old")
	stub.entries["example.com+rsa"] = []byte("old rsa")

	cache := newRenewalCache(stub, "example.com")
	assert.False(t, cache.stored())

	t.Run("hides the renewed key until it is replaced", func(t *testing.T) {
		_, err := cache.Get(ctx, "example.com")
		require.ErrorIs(t, err, autocert.ErrCacheMiss)

		data, err := cache.Get(ctx, "example.com+rsa")
		require.NoError(t, err)
		assert.Equal(t, "old rsa", string(data))
	})

	t.Run("doesn't delete the hidden entry", func(t *testing.T) {
		require.NoError(t, cache.Delete(ctx, "example.com"))
		assert.Equal(t, "old", string(stub.entries["example.com"]))
	})

	t.Run("keeps the hidden entry when storing fails", func(t *testing.T) {
		stub.putErr = errors.New("disk full")
		require.Error(t, cache.Put(ctx, "example.com", []byte("new")))
		stub.putErr = nil

		assert.False(t, cache.stored())
		assert.Equal(t, "old", string(stub.entries["example.com"]))
	})

	t.Run("stores the new entry and stops hiding it", func(t *testing.T) {
		require.NoError(t, cache.Put(ctx, "example.com", []byte("new")))
		assert.True(t, cache.stored())

		data, err := cache.Get(ctx, "example.com")
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
	})

	t.Run("passes other keys through", func(t *testing.T) {
		require.NoError(t, cache.Put(ctx, "acme_account+key", []byte("account")))
		assert.Equal(t, "account", string(stub.entries["acme_account+key"]))

		require.NoError(t, cache.Delete(ctx, "acme_account+key"))
		assert.NotContains(t, stub.entries, "acme_account+key")
	})
}

func TestRenewalClientHellosSelectCertificateType(t *testing.T) {
	cache := autocert.DirCache(t.TempDir())
	writeTestCachedCertificate(t, cache, "example.com", false)
	writeTestCachedCertificate(t, cache, "example.com+rsa", true)

	manager := &autocert.Manager{Cache: cache, HostPolicy: autocert.HostWhitelist("example.com")}

	cert, err := manager.GetCertificate(ecdsaClientHello("example.com"))
	require.NoError(t, err)
	assert.IsType(t, &ecdsa.PrivateKey{}, cert.PrivateKey)

	cert, err = manager.GetCertificate(rsaClientHello("example.com"))
	require.NoError(t, err)
	assert.IsType(t, &rsa.PrivateKey{}, cert.PrivateKey)
}

// Helpers

func writeTestCachedCertificate(t *testing.T, cache autocert.Cache, key string, useRSA bool) {
	t.Helper()

	var signer crypto.Signer
	var keyBlock *pem.Block
	if useRSA {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		signer = rsaKey
		keyBlock = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}
	} else {
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		der, err := x509.MarshalECPrivateKey(ecKey)
		require.NoError(t, err)
		signer = ecKey
		keyBlock = &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.com"},
		DNSNames:     []string{"example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, signer.Public(), signer)
	require.NoError(t, err)

	data := pem.EncodeToMemory(keyBlock)
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})...)
	require.NoError(t, cache.Put(context.Background(), key, data))
}

type stubCache struct {
	entries map[string][]byte
	putErr  error
}

func newStubCache() *stubCache {
	return &stubCache{entries: map[string][]byte{}}
}

func (c *stubCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, ok := c.entries[key]
	if !ok {
		return nil, autocert.ErrCacheMiss
	}
	return data, nil
}

func (c *stubCache) Put(ctx context.Context, key string, data []byte) error {
	if c.putErr != nil {
		return c.putErr
	}
	c.entries[key] = data
	return nil
}

func (c *stubCache) Delete(ctx context.Context, key string) error {
	delete(c.entries, key)
	return nil
}
