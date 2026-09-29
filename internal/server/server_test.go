package server

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

func TestServer_Deploying(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, true)

	testDeployTarget(t, target, server, defaultServiceOptions)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", server.HttpPort()))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_PathPrefixRoutingUsesCleanedPath(t *testing.T) {
	root := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root " + r.RequestURI))
	})
	admin := testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("admin " + r.RequestURI))
	})
	server := testServer(t, false)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"example.com"}
	testDeployTargetAs(t, "root", root, server, serviceOptions)
	serviceOptions.PathPrefixes = []string{"/admin"}
	testDeployTargetAs(t, "admin", admin, server, serviceOptions)

	serviceOptions = defaultServiceOptions
	serviceOptions.Hosts = []string{"strip.example.com"}
	testDeployTargetAs(t, "strip-root", root, server, serviceOptions)
	serviceOptions.PathPrefixes = []string{"/admin"}
	serviceOptions.StripPrefix = true
	testDeployTargetAs(t, "strip-admin", admin, server, serviceOptions)

	// Sent over a raw connection, as the Go HTTP client would clean some of
	// these paths before sending them.
	request := func(method, target, host string) (int, string) {
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpPort()))
		require.NoError(t, err)
		defer conn.Close()

		fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", method, target, host)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	get := func(target string) string {
		_, body := request(http.MethodGet, target, "example.com")
		return body
	}

	assert.Equal(t, "admin /admin/x", get("/admin/x"))
	assert.Equal(t, "admin //admin/x", get("//admin/x"))
	assert.Equal(t, "admin /./admin/x", get("/./admin/x"))
	assert.Equal(t, "admin /other/../admin/x", get("/other/../admin/x"))
	assert.Equal(t, "admin /other/%2E%2E/admin/x", get("/other/%2E%2E/admin/x"))
	assert.Equal(t, "root /admin%5C..%5Cx", get("/admin\\..\\x"))
	assert.Equal(t, "root /admin/../x", get("/admin/../x"))
	assert.Equal(t, "admin //admin/x", get("http://example.com//admin/x"))

	getStripped := func(target string) string {
		_, body := request(http.MethodGet, target, "strip.example.com")
		return body
	}

	assert.Equal(t, "admin /x?a=/../b", getStripped("/admin/x?a=/../b"))
	assert.Equal(t, "admin /x", getStripped("//admin/x"))
	assert.Equal(t, "admin /x/y/", getStripped("/admin/./x//y/"))
	assert.Equal(t, "admin /a/b/c", getStripped("/admin/a%2Fb/c"))
	assert.Equal(t, "admin /a%20b", getStripped("/admin/a%20b"))
	assert.Equal(t, "root /admin/%2e%2e/x", getStripped("/admin/%2e%2e/x"))
	assert.Equal(t, "root /admin/..%2f..%2fsecret", getStripped("/admin/..%2f..%2fsecret"))

	// Go's server answers "OPTIONS *" itself, without proxying it.
	statusCode, body := request(http.MethodOptions, "*", "example.com")
	assert.Equal(t, http.StatusOK, statusCode)
	assert.Empty(t, body)
}

func TestServer_DeployingHTTPS(t *testing.T) {
	startDeployment := func(http3Enabled bool) *Server {
		target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
		server := testServer(t, http3Enabled)

		certPath, keyPath := prepareTestCertificateFiles(t)
		serviceOptions := defaultServiceOptions
		serviceOptions.Hosts = []string{"localhost"}
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

// Helpers

func testDeployTarget(tb testing.TB, target *Target, server *Server, serviceOptions ServiceOptions) {
	tb.Helper()
	testDeployTargetAs(tb, "", target, server, serviceOptions)
}

func testDeployTargetAs(tb testing.TB, service string, target *Target, server *Server, serviceOptions ServiceOptions) {
	tb.Helper()
	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		Service:           service,
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

func TestHTTPSTLSConfig_AEADOnly(t *testing.T) {
	config := httpsTLSConfig(nil)

	assert.Equal(t, uint16(tls.VersionTLS12), config.MinVersion)
	assert.NotEmpty(t, config.CipherSuites)
	for _, id := range config.CipherSuites {
		assert.NotContains(t, tls.CipherSuiteName(id), "CBC", "CBC-mode suites must not be offered")
	}
	assert.Contains(t, config.NextProtos, acme.ALPNProto, "the ACME TLS-ALPN challenge must keep working")
}

func TestServer_HTTPSRejectsCBCCipherSuites(t *testing.T) {
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, false)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"localhost"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath

	testDeployTarget(t, target, server, serviceOptions)

	dialTLS12 := func(suites []uint16) error {
		conn, err := tls.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpsPort()), &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
			CipherSuites:       suites,
		})
		if err == nil {
			_ = conn.Close()
		}
		return err
	}

	t.Run("refuses a TLS 1.2 client that only offers CBC suites", func(t *testing.T) {
		err := dialTLS12([]uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		})
		require.Error(t, err)
	})

	t.Run("still negotiates an AEAD suite on TLS 1.2", func(t *testing.T) {
		err := dialTLS12([]uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		})
		require.NoError(t, err)
	})
}
