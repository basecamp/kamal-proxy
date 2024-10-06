package server

import (
	"crypto/tls"
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
	t.Parallel()
	certPath, keyPath := prepareTestCertificateFiles(t)

	manager, err := NewStaticCertManager(certPath, keyPath)
	require.NoError(t, err)

	cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)

	assert.Equal(t, cert.Leaf.Issuer.Organization, []string{"Acme Co"})
	assert.Nil(t, cert.Leaf.VerifyHostname("localhost:5453"))
}

func TestErrorWhenFileDoesNotExist(t *testing.T) {
	t.Parallel()
	_, err := NewStaticCertManager("testdata/cert.pem", "testdata/key.pem")
	require.ErrorContains(t, err, "unable to load certificate")
}

func TestErrorWhenKeyFormatIsInvalid(t *testing.T) {
	t.Parallel()
	certPath, keyPath := prepareTestCertificateFiles(t)

	_, err := NewStaticCertManager(keyPath, certPath) // swapped paths
	require.ErrorContains(t, err, "unable to load certificate")
}

// Helpers

func prepareTestCertificateFiles(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	certFile := path.Join(dir, "example-cert.pem")
	keyFile := path.Join(dir, "example-key.pem")

	require.NoError(t, os.WriteFile(certFile, []byte(certPem), 0644))
	require.NoError(t, os.WriteFile(keyFile, []byte(keyPem), 0644))

	return certFile, keyFile
}
