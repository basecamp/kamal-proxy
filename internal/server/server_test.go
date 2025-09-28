package server

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"testing"

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
			resp, err := testRequestUsingHTTP11(server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/1.1", resp.Proto)

			assert.Contains(t, resp.Header.Get("Alt-Svc"), "h3")
			assert.Contains(t, resp.Header.Get("Alt-Svc"), fmt.Sprintf(":%d", server.HttpsPort()))
		})

		t.Run("http/2", func(t *testing.T) {
			resp, err := testRequestUsingHTTP2(server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/2.0", resp.Proto)

			assert.Contains(t, resp.Header.Get("Alt-Svc"), "h3")
			assert.Contains(t, resp.Header.Get("Alt-Svc"), fmt.Sprintf(":%d", server.HttpsPort()))
		})

		t.Run("http/3", func(t *testing.T) {
			resp, err := testRequestUsingHTTP3(server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/3.0", resp.Proto)

			assert.Empty(t, resp.Header.Get("Alt-Svc"))
		})
	})

	t.Run("with HTTP/3 disabled", func(t *testing.T) {
		server := startDeployment(false)

		t.Run("http/1.1", func(t *testing.T) {
			resp, err := testRequestUsingHTTP11(server)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "HTTP/1.1", resp.Proto)

			assert.Empty(t, resp.Header.Get("Alt-Svc"))
		})

		t.Run("http/2", func(t *testing.T) {
			resp, err := testRequestUsingHTTP2(server)
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

func testDeployTarget(t *testing.T, target *Target, server *Server, serviceOptions ServiceOptions) {
	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		TargetURLs:     []string{target.Address()},
		DeployTimeout:  DefaultDeployTimeout,
		DrainTimeout:   DefaultDrainTimeout,
		ServiceOptions: serviceOptions,
		TargetOptions:  defaultTargetOptions,
	}, &result)

	require.NoError(t, err)
}

func testRequestUsingHTTP11(server *Server) (*http.Response, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingHTTP2(server *Server) (*http.Response, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		ForceAttemptHTTP2: true,
	}

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingHTTP3(server *Server) (*http.Response, error) {
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		},
	}
	defer transport.Close()

	return testRequestUsingTransport(server, transport)
}

func testRequestUsingTransport(server *Server, transport http.RoundTripper) (*http.Response, error) {
	client := &http.Client{
		Transport: transport,
	}

	uri := fmt.Sprintf("https://localhost:%d/", server.HttpsPort())
	return client.Get(uri)
}
