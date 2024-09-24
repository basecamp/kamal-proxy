package server

import (
	"crypto/tls"
	"os"
	"testing"

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
	certPath, keyPath, err := prepareTestCertificateFiles()
	require.NoError(t, err)
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	manager := NewStaticCertManager(certPath, keyPath)
	cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.NotNil(t, cert)
}

func TestCertificateLoadingRaceCondition(t *testing.T) {
	certPath, keyPath, err := prepareTestCertificateFiles()
	require.NoError(t, err)
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	manager := NewStaticCertManager(certPath, keyPath)
	go func() {
		manager.GetCertificate(&tls.ClientHelloInfo{})
	}()
	cert, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.NotNil(t, cert)
}

func TestCachesLoadedCertificate(t *testing.T) {
	certPath, keyPath, err := prepareTestCertificateFiles()
	require.NoError(t, err)
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	manager := NewStaticCertManager(certPath, keyPath)
	cert1, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	require.NotNil(t, cert1)

	os.Remove(certPath)
	os.Remove(keyPath)

	cert2, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.Equal(t, cert1, cert2)
}

func TestErrorWhenFileDoesNotExist(t *testing.T) {
	manager := NewStaticCertManager("testdata/cert.pem", "testdata/key.pem")
	cert1, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.ErrorContains(t, err, "no such file or directory")
	require.Nil(t, cert1)
}

func TestErrorWhenKeyFormatIsInvalid(t *testing.T) {
	certPath, keyPath, err := prepareTestCertificateFiles()
	require.NoError(t, err)
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	manager := NewStaticCertManager(keyPath, certPath)
	cert1, err := manager.GetCertificate(&tls.ClientHelloInfo{})
	require.ErrorContains(t, err, "failed to find certificate PEM data in certificate input")
	require.Nil(t, cert1)
}

func prepareTestCertificateFiles() (string, string, error) {
	certFile, err := os.CreateTemp("", "example-cert-*.pem")
	if err != nil {
		return "", "", err
	}
	defer certFile.Close()
	certFile.Write([]byte(certPem))

	keyFile, err := os.CreateTemp("", "example-key-*.pem")
	if err != nil {
		return "", "", err
	}
	defer keyFile.Close()
	keyFile.Write([]byte(keyPem))

	return certFile.Name(), keyFile.Name(), nil
}
