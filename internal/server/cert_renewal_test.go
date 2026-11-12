package server

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
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

func TestRouter_RenewCertificates_ServesRenewedCertificates(t *testing.T) {
	ca := newTestACMEServer(t)
	router, cache := testRouterWithACMEService(t, ca, "service1", []string{"example.com", "www.example.com"})

	writeTestCachedCertificate(t, cache, "example.com", false)
	writeTestCachedCertificate(t, cache, "example.com+rsa", true)
	writeTestCachedCertificate(t, cache, "www.example.com", false)

	oldECDSA := testServedLeaf(t, router, ecdsaClientHello("example.com"))
	oldRSA := testServedLeaf(t, router, rsaClientHello("example.com"))
	oldWWW := testServedLeaf(t, router, ecdsaClientHello("www.example.com"))

	require.NoError(t, router.RenewCertificates("service1", nil))
	assert.Equal(t, 3, ca.issuedCount())

	for _, hello := range []*tls.ClientHelloInfo{ecdsaClientHello("example.com"), rsaClientHello("example.com"), ecdsaClientHello("www.example.com")} {
		leaf := testServedLeaf(t, router, hello)
		assert.True(t, ca.issued(leaf), "serves the renewed certificate for %s", hello.ServerName)
	}
	assert.False(t, ca.issued(oldECDSA))
	assert.False(t, ca.issued(oldRSA))
	assert.False(t, ca.issued(oldWWW))
	assert.IsType(t, &rsa.PublicKey{}, testServedLeaf(t, router, rsaClientHello("example.com")).PublicKey)
}

func TestRouter_RenewCertificates_ReloadsWhenRSARenewalFails(t *testing.T) {
	ca := newTestACMEServer(t)
	ca.rejectRSA = true
	router, cache := testRouterWithACMEService(t, ca, "service1", []string{"example.com", "www.example.com"})

	writeTestCachedCertificate(t, cache, "example.com", false)
	writeTestCachedCertificate(t, cache, "example.com+rsa", true)
	writeTestCachedCertificate(t, cache, "www.example.com", false)

	oldRSA := testServedLeaf(t, router, rsaClientHello("example.com"))
	oldWWW := testServedLeaf(t, router, ecdsaClientHello("www.example.com"))

	err := router.RenewCertificates("service1", nil)
	require.ErrorContains(t, err, "unable to renew certificate for example.com")
	require.ErrorContains(t, err, "RSA keys are not accepted")
	require.ErrorContains(t, err, "(renewed: example.com)")

	assert.True(t, ca.issued(testServedLeaf(t, router, ecdsaClientHello("example.com"))), "serves the renewed ECDSA certificate")
	assert.Equal(t, oldRSA.SerialNumber, testServedLeaf(t, router, rsaClientHello("example.com")).SerialNumber, "keeps the existing RSA certificate")
	assert.Equal(t, oldWWW.SerialNumber, testServedLeaf(t, router, ecdsaClientHello("www.example.com")).SerialNumber, "doesn't renew later hosts")
}

func TestRouter_RenewCertificates_NormalizesHosts(t *testing.T) {
	ca := newTestACMEServer(t)
	router, cache := testRouterWithACMEService(t, ca, "service1", []string{"example.com", "www.example.com"})

	writeTestCachedCertificate(t, cache, "example.com", false)
	writeTestCachedCertificate(t, cache, "example.com+rsa", true)

	require.NoError(t, router.RenewCertificates("service1", []string{"Example.COM.", "example.com"}))
	assert.Equal(t, 2, ca.issuedCount())

	assert.True(t, ca.issued(testServedLeaf(t, router, ecdsaClientHello("example.com"))))
	assert.True(t, ca.issued(testServedLeaf(t, router, rsaClientHello("example.com"))))
}

func TestRouter_RenewCertificates_SubPathService(t *testing.T) {
	ca := newTestACMEServer(t)
	router, cache := testRouterWithACMEService(t, ca, "web", []string{"example.com"})
	_, target := testBackend(t, "api", http.StatusOK)

	apiOptions := defaultServiceOptions
	apiOptions.Hosts = []string{"example.com"}
	apiOptions.PathPrefixes = []string{"/api"}
	require.NoError(t, router.DeployService("api", []string{target}, defaultEmptyReaders, apiOptions, defaultTargetOptions, defaultDeploymentOptions))

	writeTestCachedCertificate(t, cache, "example.com", false)
	old := testServedLeaf(t, router, ecdsaClientHello("example.com"))

	err := router.RenewCertificates("api", nil)
	require.ErrorIs(t, err, ErrorCertRenewalNotRootPath)
	require.ErrorContains(t, err, "web")
	assert.Equal(t, 0, ca.issuedCount())
	assert.Equal(t, old.SerialNumber, testServedLeaf(t, router, ecdsaClientHello("example.com")).SerialNumber)

	require.NoError(t, router.RenewCertificates("web", nil))
	assert.True(t, ca.issued(testServedLeaf(t, router, ecdsaClientHello("example.com"))))
}

func TestRouter_RenewCertificates_OnDemandHostServedByAnotherService(t *testing.T) {
	router := testRouter(t)
	_, target := testBackend(t, "first", http.StatusOK)

	onDemandOptions := defaultServiceOptions
	onDemandOptions.TLSEnabled = true
	onDemandOptions.TLSOnDemandURL = "/allow"
	onDemandOptions.ACMECachePath = t.TempDir()
	require.NoError(t, router.DeployService("ondemand", []string{target}, defaultEmptyReaders, onDemandOptions, defaultTargetOptions, defaultDeploymentOptions))

	otherOptions := defaultServiceOptions
	otherOptions.Hosts = []string{"other.example.com"}
	require.NoError(t, router.DeployService("other", []string{target}, defaultEmptyReaders, otherOptions, defaultTargetOptions, defaultDeploymentOptions))

	require.ErrorIs(t, router.RenewCertificates("ondemand", nil), ErrorCertRenewalHostRequired)

	err := router.RenewCertificates("ondemand", []string{"other.example.com"})
	require.ErrorIs(t, err, ErrorCertRenewalUnknownHost)
	require.ErrorContains(t, err, "other.example.com")
}

func TestService_RenewCertificate_NotStored(t *testing.T) {
	ca := newTestACMEServer(t)
	service := &Service{name: "test"}
	options := ServiceOptions{TLSEnabled: true, Hosts: []string{"example.com"}, ACMEDirectory: ca.URL + "/directory"}

	cache := newStubCache()
	cache.putErrKey = "example.com"
	cache.putErr = errors.New("disk full")

	stored, err := service.renewCertificate(options, cache, "example.com")
	require.ErrorIs(t, err, ErrorCertRenewalNotStored)
	require.ErrorContains(t, err, "disk full")
	assert.Empty(t, stored)
	assert.NotContains(t, cache.entries, "example.com")
}

func TestCertificateHostsToRenew(t *testing.T) {
	options := ServiceOptions{TLSEnabled: true, Hosts: []string{"example.com", "www.example.com", "bücher.example"}}

	t.Run("defaults to the service's hosts", func(t *testing.T) {
		hosts, err := certificateHostsToRenew(options, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"example.com", "www.example.com", "xn--bcher-kva.example"}, hosts)
	})

	t.Run("accepts a subset of the service's hosts, normalized", func(t *testing.T) {
		hosts, err := certificateHostsToRenew(options, []string{"WWW.example.com.", "www.example.com", "BÜCHER.example"})
		require.NoError(t, err)
		assert.Equal(t, []string{"www.example.com", "xn--bcher-kva.example"}, hosts)
	})

	t.Run("rejects hosts the service doesn't serve", func(t *testing.T) {
		_, err := certificateHostsToRenew(options, []string{"example.com", "other.example.com"})
		require.ErrorIs(t, err, ErrorCertRenewalUnknownHost)
	})

	t.Run("rejects invalid hosts", func(t *testing.T) {
		_, err := certificateHostsToRenew(options, []string{"exa mple.com"})
		require.ErrorIs(t, err, ErrorCertRenewalInvalidHost)
	})

	t.Run("requires a host for TLS on-demand services", func(t *testing.T) {
		onDemandOptions := ServiceOptions{TLSEnabled: true, TLSOnDemandURL: "/allow", Hosts: []string{""}}

		_, err := certificateHostsToRenew(onDemandOptions, nil)
		require.ErrorIs(t, err, ErrorCertRenewalHostRequired)

		hosts, err := certificateHostsToRenew(onDemandOptions, []string{"Customer.Example.com"})
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
	require.ErrorIs(t, cache.storeError(), ErrorCertRenewalNotStored)

	t.Run("hides the renewed key until it is replaced", func(t *testing.T) {
		_, err := cache.Get(ctx, "example.com")
		require.ErrorIs(t, err, autocert.ErrCacheMiss)

		data, err := cache.Get(ctx, "example.com+rsa")
		require.NoError(t, err)
		assert.Equal(t, "old rsa", string(data))
	})

	t.Run("keeps the hidden entry when storing fails", func(t *testing.T) {
		stub.putErr = errors.New("disk full")
		require.Error(t, cache.Put(ctx, "example.com", []byte("new")))
		stub.putErr = nil

		err := cache.storeError()
		require.ErrorIs(t, err, ErrorCertRenewalNotStored)
		require.ErrorContains(t, err, "disk full")
		assert.Equal(t, "old", string(stub.entries["example.com"]))
	})

	t.Run("stores the new entry and stops hiding it", func(t *testing.T) {
		require.NoError(t, cache.Put(ctx, "example.com", []byte("new")))
		require.NoError(t, cache.storeError())

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

func TestRenewalTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	transport := &renewalTransport{}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	resp.Body.Close()

	transport.close()

	_, err = client.Get(server.URL)
	require.ErrorIs(t, err, errorCertRenewalFinished)
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

func TestService_AnswersChallengesStoredByRenewalManager(t *testing.T) {
	serviceOptions := ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, ACMECachePath: t.TempDir()}
	service := testCreateService(t, serviceOptions, defaultTargetOptions)
	cache := autocert.DirCache(serviceOptions.ScopedCachePath())

	// A renewal manager stores the tls-alpn-01 challenge certificate in the
	// shared cache; the service's manager must serve it to the CA.
	writeTestCachedCertificate(t, cache, "example.com+token", false)

	cert, err := service.certManager.GetCertificate(&tls.ClientHelloInfo{
		ServerName:      "example.com",
		SupportedProtos: []string{acme.ALPNProto},
	})
	require.NoError(t, err)
	assert.IsType(t, &ecdsa.PrivateKey{}, cert.PrivateKey)

	// Likewise for http-01 tokens.
	require.NoError(t, cache.Put(context.Background(), "token123+http-01", []byte("key-authorization")))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/.well-known/acme-challenge/token123", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Equal(t, "key-authorization", w.Body.String())
}

// Helpers

func writeTestCachedCertificate(t *testing.T, cache autocert.Cache, key string, useRSA bool) {
	t.Helper()

	domain := strings.TrimSuffix(key, "+rsa")
	domain = strings.TrimSuffix(domain, "+token")

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
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
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
	entries   map[string][]byte
	putErr    error
	putErrKey string
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
	if c.putErr != nil && (c.putErrKey == "" || c.putErrKey == key) {
		return c.putErr
	}
	c.entries[key] = data
	return nil
}

func (c *stubCache) Delete(ctx context.Context, key string) error {
	delete(c.entries, key)
	return nil
}

func testRouterWithACMEService(t *testing.T, ca *testACMEServer, name string, hosts []string) (*Router, autocert.Cache) {
	t.Helper()

	router := testRouter(t)
	_, target := testBackend(t, name, http.StatusOK)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = hosts
	serviceOptions.TLSEnabled = true
	serviceOptions.ACMEDirectory = ca.URL + "/directory"
	serviceOptions.ACMECachePath = t.TempDir()
	require.NoError(t, router.DeployService(name, []string{target}, defaultEmptyReaders, serviceOptions, defaultTargetOptions, defaultDeploymentOptions))

	return router, autocert.DirCache(serviceOptions.ScopedCachePath())
}

func testServedLeaf(t *testing.T, router *Router, hello *tls.ClientHelloInfo) *x509.Certificate {
	t.Helper()

	cert, err := router.GetCertificate(hello)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	return leaf
}

// testACMEServer is a minimal ACME CA. Orders are created ready to be
// finalized, so no challenges are needed, and certificates are signed by a
// test CA.
type testACMEServer struct {
	*httptest.Server

	rejectRSA bool

	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate

	lock    sync.Mutex
	nonce   int
	serials []*big.Int
	certs   [][]byte
}

func newTestACMEServer(t *testing.T) *testACMEServer {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test ACME CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, template, template, caKey.Public(), caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	s := &testACMEServer{caKey: caKey, caCert: caCert}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /directory", func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"newNonce":   s.URL + "/nonce",
			"newAccount": s.URL + "/account",
			"newOrder":   s.URL + "/order",
			"revokeCert": s.URL + "/revoke",
			"keyChange":  s.URL + "/key-change",
		})
	})
	mux.HandleFunc("/nonce", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", s.URL+"/account/1")
		s.writeJSON(w, http.StatusCreated, map[string]string{"status": "valid"})
	})
	mux.HandleFunc("POST /order", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", s.URL+"/order/1")
		s.writeJSON(w, http.StatusCreated, map[string]any{"status": "ready", "authorizations": []string{}, "finalize": s.URL + "/finalize"})
	})
	mux.HandleFunc("POST /finalize", s.finalize)
	mux.HandleFunc("POST /cert/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		s.lock.Lock()
		defer s.lock.Unlock()
		if err != nil || id >= len(s.certs) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		w.Write(s.certs[id])
	})

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lock.Lock()
		s.nonce++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", s.nonce))
		s.lock.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)

	return s
}

func (s *testACMEServer) finalize(w http.ResponseWriter, r *http.Request) {
	var jws struct{ Payload string }
	var payload struct{ CSR string }
	csr, err := func() (*x509.CertificateRequest, error) {
		if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
			return nil, err
		}
		data, err := base64.RawURLEncoding.DecodeString(jws.Payload)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		der, err := base64.RawURLEncoding.DecodeString(payload.CSR)
		if err != nil {
			return nil, err
		}
		return x509.ParseCertificateRequest(der)
	}()
	if err != nil {
		s.writeProblem(w, "badCSR", err.Error())
		return
	}

	if _, ok := csr.PublicKey.(*rsa.PublicKey); ok && s.rejectRSA {
		s.writeProblem(w, "badCSR", "RSA keys are not accepted")
		return
	}

	s.lock.Lock()
	id := len(s.certs)
	serial := big.NewInt(int64(1000 + id))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: csr.DNSNames[0]},
		DNSNames:     csr.DNSNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		s.lock.Unlock()
		s.writeProblem(w, "serverInternal", err.Error())
		return
	}
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw})...)
	s.certs = append(s.certs, chain)
	s.serials = append(s.serials, serial)
	s.lock.Unlock()

	w.Header().Set("Location", s.URL+"/order/1")
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "valid", "certificate": fmt.Sprintf("%s/cert/%d", s.URL, id)})
}

func (s *testACMEServer) issued(leaf *x509.Certificate) bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return leaf.Issuer.CommonName == s.caCert.Subject.CommonName &&
		slices.ContainsFunc(s.serials, func(serial *big.Int) bool { return serial.Cmp(leaf.SerialNumber) == 0 })
}

func (s *testACMEServer) issuedCount() int {
	s.lock.Lock()
	defer s.lock.Unlock()
	return len(s.certs)
}

func (s *testACMEServer) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *testACMEServer) writeProblem(w http.ResponseWriter, typ, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{"type": "urn:ietf:params:acme:error:" + typ, "detail": detail})
}
