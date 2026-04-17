package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Deploying(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, true)

	testDeployTarget(t, target, server, defaultServiceOptions)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", server.HttpPort()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_DeployingHTTPS(t *testing.T) {
	startDeployment := func(http3Enabled bool) *Server {
		target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
		server := testServer(t, http3Enabled)

		certPath, keyPath := prepareTestCertificateFiles(t)
		serviceOptions := defaultServiceOptions
		serviceOptions.TLSEnabled = true
		serviceOptions.TLSCertificatePath = certPath
		serviceOptions.TLSPrivateKeyPath = keyPath
		serviceOptions.TLSRedirect = true

		testDeployTarget(t, target, server, serviceOptions)
		return server
	}

	t.Run("with HTTP/3 enabled", func(t *testing.T) {
		server := startDeployment(true)

		t.Run("http/1.1", func(t *testing.T) {
			resp, err := testRequestUsingHTTP11(t, server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/1.1", resp.Proto)

			assert.Contains(t, resp.Header.Get("Alt-Svc"), "h3")
			assert.Contains(t, resp.Header.Get("Alt-Svc"), fmt.Sprintf(":%d", server.HttpsPort()))
		})

		t.Run("http/2", func(t *testing.T) {
			resp, err := testRequestUsingHTTP2(t, server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/2.0", resp.Proto)

			assert.Contains(t, resp.Header.Get("Alt-Svc"), "h3")
			assert.Contains(t, resp.Header.Get("Alt-Svc"), fmt.Sprintf(":%d", server.HttpsPort()))
		})

		t.Run("http/3", func(t *testing.T) {
			resp, err := testRequestUsingHTTP3(t, server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/3.0", resp.Proto)

			assert.Empty(t, resp.Header.Get("Alt-Svc"))
		})
	})

	t.Run("with HTTP/3 disabled", func(t *testing.T) {
		server := startDeployment(false)

		t.Run("http/1.1", func(t *testing.T) {
			resp, err := testRequestUsingHTTP11(t, server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/1.1", resp.Proto)

			assert.Empty(t, resp.Header.Get("Alt-Svc"))
		})

		t.Run("http/2", func(t *testing.T) {
			resp, err := testRequestUsingHTTP2(t, server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/2.0", resp.Proto)

			assert.Empty(t, resp.Header.Get("Alt-Svc"))
		})

		t.Run("http/3", func(t *testing.T) {
			// Ensure we don't already have a UDP listener for HTTP/3
			addr := fmt.Sprintf(":%d", server.HttpsPort())
			con, err := net.ListenPacket("udp", addr)
			require.NoError(t, err)
			con.Close()
		})
	})
}

func TestServer_DeployingHTTPSWithClientCA(t *testing.T) {
	ca := generateTestCA(t)
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, false)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.Hosts = []string{"localhost"}
	serviceOptions.TLSClientCACertificatePath = ca.certPath

	testDeployTarget(t, target, server, serviceOptions)

	t.Run("rejects request without client certificate", func(t *testing.T) {
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		_, err := (&http.Client{Transport: transport}).Get(fmt.Sprintf("https://localhost:%d/", server.HttpsPort()))
		assert.Error(t, err)
	})

	t.Run("rejects client certificate from unknown CA", func(t *testing.T) {
		wrongCA := generateTestCA(t)
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{wrongCA.clientCert},
			},
		}
		_, err := (&http.Client{Transport: transport}).Get(fmt.Sprintf("https://localhost:%d/", server.HttpsPort()))
		assert.Error(t, err)
	})

	t.Run("accepts client certificate from trusted CA", func(t *testing.T) {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{ca.clientCert},
			},
		}
		resp, err := (&http.Client{Transport: transport}).Get(fmt.Sprintf("https://localhost:%d/", server.HttpsPort()))
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// Helpers

func testDeployTarget(tb testing.TB, target *Target, server *Server, serviceOptions ServiceOptions) {
	tb.Helper()
	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		TargetURLs:        []string{target.Address()},
		DeploymentOptions: defaultDeploymentOptions,
		ServiceOptions:    serviceOptions,
		TargetOptions:     defaultTargetOptions,
	}, &result)

	require.NoError(tb, err)
}

func testRequestUsingHTTP11(tb testing.TB, server *Server) (*http.Response, error) {
	tb.Helper()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingHTTP2(tb testing.TB, server *Server) (*http.Response, error) {
	tb.Helper()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		ForceAttemptHTTP2: true,
	}

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingHTTP3(tb testing.TB, server *Server) (*http.Response, error) {
	tb.Helper()
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		},
	}
	tb.Cleanup(func() { _ = transport.Close() })

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingTransport(server *Server, transport http.RoundTripper) (*http.Response, error) {
	client := &http.Client{
		Transport: transport,
	}

	uri := fmt.Sprintf("https://localhost:%d/", server.HttpsPort())
	return client.Get(uri)
}

type testCAFixture struct {
	certPath   string
	clientCert tls.Certificate
}

func generateTestCA(t *testing.T) testCAFixture {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test CA"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, caPEM, 0644))

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"Test Client"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	require.NoError(t, err)

	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	require.NoError(t, err)

	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER})

	clientTLSCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	require.NoError(t, err)

	return testCAFixture{certPath: caPath, clientCert: clientTLSCert}
}
