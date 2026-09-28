package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const certPem = `-----BEGIN CERTIFICATE-----
MIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw
DgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow
EjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d
7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B
5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr
BgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1
NDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l
Wf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBmLiAFQOyTDT+/wQc
6MF9+Yw1Yy0t
-----END CERTIFICATE-----`

const keyPem = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIIrYSSNQFaA2Hwf1duRSxKtLYX5CB04fSeQ6tF1aY/PuoAoGCCqGSM49
AwEHoUQDQgAEPR3tU2Fta9ktY+6P9G0cWO+0kETA6SFs38GecTyudlHz6xvCdz8q
EKTcWGekdmdDPsHloRNtsiCa697B2O9IFA==
-----END EC PRIVATE KEY-----`

func TestCertificateLoading(t *testing.T) {
	certPath, keyPath := prepareTestCertificateFiles(t)

	manager, err := NewStaticCertManager(certPath, keyPath)
	require.NoError(t, err)

	cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)

	assert.Equal(t, cert.Leaf.Issuer.Organization, []string{"Acme Co"})
	assert.Nil(t, cert.Leaf.VerifyHostname("localhost:5453"))
}

func TestErrorWhenFileDoesNotExist(t *testing.T) {
	_, err := NewStaticCertManager("testdata/cert.pem", "testdata/key.pem")
	require.ErrorContains(t, err, "unable to load certificate")
}

func TestErrorWhenKeyFormatIsInvalid(t *testing.T) {
	certPath, keyPath := prepareTestCertificateFiles(t)

	_, err := NewStaticCertManager(keyPath, certPath) // swapped paths
	require.ErrorContains(t, err, "unable to load certificate")
}

func TestClientCALoading(t *testing.T) {
	ca := generateTestCA(t)

	clientCA, err := NewClientCA(ca.certPath)
	require.NoError(t, err)

	_, err = ca.clientCert.Leaf.Verify(x509.VerifyOptions{
		Roots:     clientCA.CertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	assert.NoError(t, err)
}

func TestClientCALoadingErrorWhenFileDoesNotExist(t *testing.T) {
	_, err := NewClientCA("testdata/ca.pem")
	require.ErrorIs(t, err, ErrorUnableToLoadClientCACertificate)
}

func TestClientCALoadingErrorWhenFileIsInvalid(t *testing.T) {
	_, keyPath := prepareTestCertificateFiles(t)

	_, err := NewClientCA(keyPath)
	require.ErrorIs(t, err, ErrorUnableToLoadClientCACertificate)
}

func TestClientCATrustsConnection(t *testing.T) {
	ca := generateTestCA(t)
	otherCA := generateTestCA(t)

	clientCA, err := NewClientCA(ca.certPath)
	require.NoError(t, err)

	t.Run("chain anchored in the CA", func(t *testing.T) {
		state := testConnectionStateVerifiedBy(t, ca)
		assert.True(t, clientCA.TrustsConnection(state))
	})

	t.Run("chain anchored in another CA", func(t *testing.T) {
		state := testConnectionStateVerifiedBy(t, otherCA)
		assert.False(t, clientCA.TrustsConnection(state))
	})

	t.Run("no verified chains", func(t *testing.T) {
		state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{ca.clientCert.Leaf}}
		assert.False(t, clientCA.TrustsConnection(state))
	})

	t.Run("empty chain", func(t *testing.T) {
		state := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{}}}
		assert.False(t, clientCA.TrustsConnection(state))
	})

	const clientCertIndex, caCertIndex = 0, 1

	withChainCertificate := func(state *tls.ConnectionState, index int, change func(*x509.Certificate)) *tls.ConnectionState {
		chain := slices.Clone(state.VerifiedChains[0])
		changedCert := *chain[index]
		change(&changedCert)
		chain[index] = &changedCert

		return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{chain}}
	}

	t.Run("client certificate expired", func(t *testing.T) {
		state := withChainCertificate(testConnectionStateVerifiedBy(t, ca), clientCertIndex, func(cert *x509.Certificate) {
			cert.NotAfter = time.Now().Add(-time.Minute)
		})
		assert.False(t, clientCA.TrustsConnection(state))
	})

	t.Run("client certificate not yet valid", func(t *testing.T) {
		state := withChainCertificate(testConnectionStateVerifiedBy(t, ca), clientCertIndex, func(cert *x509.Certificate) {
			cert.NotBefore = time.Now().Add(time.Minute)
		})
		assert.False(t, clientCA.TrustsConnection(state))
	})

	t.Run("CA certificate expired", func(t *testing.T) {
		state := withChainCertificate(testConnectionStateVerifiedBy(t, ca), caCertIndex, func(cert *x509.Certificate) {
			cert.NotAfter = time.Now().Add(-time.Minute)
		})
		assert.False(t, clientCA.TrustsConnection(state))
	})

	t.Run("CA reloaded from the same file", func(t *testing.T) {
		reloadedClientCA, err := NewClientCA(ca.certPath)
		require.NoError(t, err)

		state := testConnectionStateVerifiedBy(t, ca)
		assert.True(t, reloadedClientCA.TrustsConnection(state))
	})
}

func TestParseCACertificates(t *testing.T) {
	caPEM := readTestCAPEM(t)
	otherCAPEM := readTestCAPEM(t)

	parse := func(pemData string) ([]*x509.Certificate, error) {
		return parseCACertificates([]byte(pemData))
	}

	t.Run("single certificate", func(t *testing.T) {
		certs, err := parse(caPEM)
		require.NoError(t, err)
		assert.Len(t, certs, 1)
	})

	t.Run("certificate bundle", func(t *testing.T) {
		certs, err := parse(caPEM + otherCAPEM)
		require.NoError(t, err)
		assert.Len(t, certs, 2)
	})

	t.Run("text outside PEM blocks", func(t *testing.T) {
		certs, err := parse("# Test CA\n" + caPEM + "\n# Other CA\n" + otherCAPEM + "\n# End\n")
		require.NoError(t, err)
		assert.Len(t, certs, 2)
	})

	t.Run("empty file", func(t *testing.T) {
		_, err := parse("")
		assert.ErrorContains(t, err, "no certificates found")
	})

	t.Run("text only", func(t *testing.T) {
		_, err := parse("not a certificate\n")
		assert.ErrorContains(t, err, "no certificates found")
	})

	t.Run("private key", func(t *testing.T) {
		_, err := parse(caPEM + keyPem)
		assert.ErrorContains(t, err, `unexpected PEM block type "EC PRIVATE KEY"`)
	})

	t.Run("invalid certificate", func(t *testing.T) {
		invalid := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}))

		_, err := parse(caPEM + invalid)
		assert.Error(t, err)
	})

	t.Run("truncated last block", func(t *testing.T) {
		truncated := otherCAPEM[:len(otherCAPEM)/2]

		_, err := parse(caPEM + truncated)
		assert.ErrorContains(t, err, "malformed PEM block")
	})

	t.Run("malformed block followed by a valid one", func(t *testing.T) {
		truncated := otherCAPEM[:len(otherCAPEM)/2]

		_, err := parse(truncated + caPEM)
		assert.ErrorContains(t, err, "malformed PEM block")
	})
}

func BenchmarkClientCA_TrustsConnection(b *testing.B) {
	ca := generateTestCA(b)
	clientCA, err := NewClientCA(ca.certPath)
	require.NoError(b, err)
	state := testConnectionStateVerifiedBy(b, ca)

	for b.Loop() {
		clientCA.TrustsConnection(state)
	}
}

func TestClientCATrustsConnectionThroughIntermediateCA(t *testing.T) {
	chain := generateTestCAChain(t)
	unrelatedCA := generateTestCA(t)

	clientCAFor := func(caPath string) *ClientCA {
		clientCA, err := NewClientCA(caPath)
		require.NoError(t, err)
		return clientCA
	}
	expired := func(cert *x509.Certificate) *x509.Certificate {
		expiredCert := *cert
		expiredCert.NotAfter = time.Now().Add(-time.Minute)
		return &expiredCert
	}
	connectionWithChain := func(certs ...*x509.Certificate) *tls.ConnectionState {
		return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{certs}}
	}

	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(chain.root)
	intermediates.AddCert(chain.intermediate)
	verifiedChains, err := chain.leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err)
	require.Equal(t, [][]*x509.Certificate{{chain.leaf, chain.intermediate, chain.root}}, verifiedChains)

	verifiedByRoot := &tls.ConnectionState{VerifiedChains: verifiedChains}

	t.Run("CA trusting the root", func(t *testing.T) {
		assert.True(t, clientCAFor(chain.rootPath).TrustsConnection(verifiedByRoot))
	})

	t.Run("CA trusting only the intermediate", func(t *testing.T) {
		assert.True(t, clientCAFor(chain.intermediatePath).TrustsConnection(verifiedByRoot))
	})

	t.Run("CA trusting only the intermediate, with the root expired", func(t *testing.T) {
		state := connectionWithChain(chain.leaf, chain.intermediate, expired(chain.root))
		assert.True(t, clientCAFor(chain.intermediatePath).TrustsConnection(state))
	})

	t.Run("CA trusting only the intermediate, with the intermediate expired", func(t *testing.T) {
		state := connectionWithChain(chain.leaf, expired(chain.intermediate), chain.root)
		assert.False(t, clientCAFor(chain.intermediatePath).TrustsConnection(state))
	})

	t.Run("CA trusting the root, with the root expired", func(t *testing.T) {
		state := connectionWithChain(chain.leaf, chain.intermediate, expired(chain.root))
		assert.False(t, clientCAFor(chain.rootPath).TrustsConnection(state))
	})

	t.Run("unrelated CA", func(t *testing.T) {
		assert.False(t, clientCAFor(unrelatedCA.certPath).TrustsConnection(verifiedByRoot))
	})
}

// Helpers

type testCAChainFixture struct {
	root             *x509.Certificate
	intermediate     *x509.Certificate
	leaf             *x509.Certificate
	rootPath         string
	intermediatePath string
}

func generateTestCAChain(t testing.TB) testCAChainFixture {
	t.Helper()

	issue := func(template, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		if parent == nil {
			parent, parentKey = template, key
		}

		der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
		require.NoError(t, err)

		cert, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		return cert, key
	}
	caTemplate := func(serialNumber int64, organization string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber:          big.NewInt(serialNumber),
			Subject:               pkix.Name{Organization: []string{organization}},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			IsCA:                  true,
			KeyUsage:              x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
		}
	}
	writePEM := func(cert *x509.Certificate) string {
		certPath := path.Join(t.TempDir(), "ca.pem")
		require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0644))
		return certPath
	}

	root, rootKey := issue(caTemplate(1, "Test Root CA"), nil, nil)
	intermediate, intermediateKey := issue(caTemplate(2, "Test Intermediate CA"), root, rootKey)
	leaf, _ := issue(&x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{Organization: []string{"Test Client"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, intermediate, intermediateKey)

	return testCAChainFixture{
		root:             root,
		intermediate:     intermediate,
		leaf:             leaf,
		rootPath:         writePEM(root),
		intermediatePath: writePEM(intermediate),
	}
}

func testConnectionStateVerifiedBy(t testing.TB, ca testCAFixture) *tls.ConnectionState {
	t.Helper()

	clientCA, err := NewClientCA(ca.certPath)
	require.NoError(t, err)

	verifiedChains, err := ca.clientCert.Leaf.Verify(x509.VerifyOptions{
		Roots:     clientCA.CertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	require.NoError(t, err)

	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{ca.clientCert.Leaf},
		VerifiedChains:   verifiedChains,
	}
}

func readTestCAPEM(t *testing.T) string {
	t.Helper()

	pemData, err := os.ReadFile(generateTestCA(t).certPath)
	require.NoError(t, err)

	return string(pemData)
}

func prepareTestCertificateFiles(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	certFile := path.Join(dir, "example-cert.pem")
	keyFile := path.Join(dir, "example-key.pem")

	require.NoError(t, os.WriteFile(certFile, []byte(certPem), 0644))
	require.NoError(t, os.WriteFile(keyFile, []byte(keyPem), 0644))

	return certFile, keyFile
}
