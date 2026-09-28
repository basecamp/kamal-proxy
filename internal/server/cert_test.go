package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path"
	"testing"

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

// Helpers

func testConnectionStateVerifiedBy(t *testing.T, ca testCAFixture) *tls.ConnectionState {
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
