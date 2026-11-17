package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
)

func TestServer_DeployingHTTPSWithClientCA(t *testing.T) {
	ca := generateTestCA(t)
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, true)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.Hosts = []string{"localhost"}
	serviceOptions.TLSClientCAPath = ca.certPath

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
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("rejects request without SNI or client certificate", func(t *testing.T) {
		transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		_, err := (&http.Client{Transport: transport}).Get(fmt.Sprintf("https://127.0.0.1:%d/", server.HttpsPort()))
		assert.Error(t, err)
	})

	t.Run("accepts request without SNI with trusted client certificate", func(t *testing.T) {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{ca.clientCert},
			},
		}
		resp, err := (&http.Client{Transport: transport}).Get(fmt.Sprintf("https://127.0.0.1:%d/", server.HttpsPort()))
		require.NoError(t, err)
		defer resp.Body.Close()
	})

	dialTLS12WithProtos := func(protos []string) (*tls.Conn, error) {
		return tls.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpsPort()), &tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS12,
			NextProtos:         protos,
		})
	}

	t.Run("rejects ACME ALPN combined with other protocols without client certificate", func(t *testing.T) {
		conn, err := dialTLS12WithProtos([]string{acme.ALPNProto, "http/1.1"})
		if err == nil {
			conn.Close()
		}
		assert.Error(t, err)
	})

	t.Run("negotiates HTTP/2 with trusted client certificate", func(t *testing.T) {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{ca.clientCert},
			},
			ForceAttemptHTTP2: true,
		}
		resp, err := testRequestUsingTransport(server, transport)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "HTTP/2.0", resp.Proto)
	})

	t.Run("negotiates HTTP/3 with trusted client certificate", func(t *testing.T) {
		transport := &http3.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h3"},
				Certificates:       []tls.Certificate{ca.clientCert},
			},
		}
		t.Cleanup(func() { _ = transport.Close() })

		resp, err := testRequestUsingTransport(server, transport)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "HTTP/3.0", resp.Proto)
	})

	t.Run("refuses CBC suites with trusted client certificate", func(t *testing.T) {
		conn, err := tls.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpsPort()), &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
				tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			},
			Certificates: []tls.Certificate{ca.clientCert},
		})
		if err == nil {
			conn.Close()
		}
		assert.Error(t, err)
	})
}

func TestServer_ClientCAEnforcedWhenSNIDiffersFromHost(t *testing.T) {
	ca := generateTestCA(t)
	server := testServer(t, false)
	certPath, keyPath := prepareTestCertificateFiles(t)

	deploy := func(name string, hosts []string, clientCAPath string) {
		target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})

		serviceOptions := defaultServiceOptions
		serviceOptions.TLSEnabled = true
		serviceOptions.TLSCertificatePath = certPath
		serviceOptions.TLSPrivateKeyPath = keyPath
		serviceOptions.Hosts = hosts
		serviceOptions.TLSClientCAPath = clientCAPath

		var result bool
		err := server.commandHandler.Deploy(DeployArgs{
			Service:           name,
			TargetURLs:        []string{target.Address()},
			DeploymentOptions: defaultDeploymentOptions,
			ServiceOptions:    serviceOptions,
			TargetOptions:     defaultTargetOptions,
		}, &result)
		require.NoError(t, err)
	}

	deploy("mtls", []string{"localhost", "alt.example.com"}, ca.certPath)
	deploy("public", []string{"public.example.com"}, "")

	request := func(sni, host string, certificates []tls.Certificate) *http.Response {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         sni,
				Certificates:       certificates,
			},
		}
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://localhost:%d/", server.HttpsPort()), nil)
		require.NoError(t, err)
		req.Host = host

		resp, err := (&http.Client{Transport: transport}).Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	t.Run("rejects request to mTLS host over connection for host without mTLS", func(t *testing.T) {
		resp := request("public.example.com", "localhost", nil)
		assert.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode)
	})

	t.Run("rejects request to mTLS host over connection for host without mTLS, even with client certificate", func(t *testing.T) {
		resp := request("public.example.com", "localhost", []tls.Certificate{ca.clientCert})
		assert.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode)
	})

	t.Run("accepts request to mTLS host over connection for another host of the same service", func(t *testing.T) {
		resp := request("alt.example.com", "localhost", []tls.Certificate{ca.clientCert})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("accepts request to host without mTLS over mTLS connection", func(t *testing.T) {
		resp := request("localhost", "public.example.com", []tls.Certificate{ca.clientCert})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestServer_ClientCAEnforcedForPathServices(t *testing.T) {
	ca := generateTestCA(t)
	server := testServer(t, false)
	certPath, keyPath := prepareTestCertificateFiles(t)

	deploy := func(name string, serviceOptions ServiceOptions, handler http.HandlerFunc) {
		target := testTarget(t, handler)

		var result bool
		err := server.commandHandler.Deploy(DeployArgs{
			Service:           name,
			TargetURLs:        []string{target.Address()},
			DeploymentOptions: defaultDeploymentOptions,
			ServiceOptions:    serviceOptions,
			TargetOptions:     defaultTargetOptions,
		}, &result)
		require.NoError(t, err)
	}

	tlsServiceOptions := defaultServiceOptions
	tlsServiceOptions.TLSEnabled = true
	tlsServiceOptions.TLSCertificatePath = certPath
	tlsServiceOptions.TLSPrivateKeyPath = keyPath

	clientCAServiceOptions := tlsServiceOptions
	clientCAServiceOptions.Hosts = []string{"localhost"}
	clientCAServiceOptions.TLSClientCAPath = ca.certPath

	publicServiceOptions := tlsServiceOptions
	publicServiceOptions.Hosts = []string{"public.example.com"}

	pathServiceOptions := defaultServiceOptions
	pathServiceOptions.Hosts = []string{"localhost"}
	pathServiceOptions.PathPrefixes = []string{"/api"}

	var pathTargetReached atomic.Bool
	deploy("mtls", clientCAServiceOptions, func(w http.ResponseWriter, r *http.Request) {})
	deploy("public", publicServiceOptions, func(w http.ResponseWriter, r *http.Request) {})
	deploy("api", pathServiceOptions, func(w http.ResponseWriter, r *http.Request) {
		recordRequestsExceptHealthChecks(&pathTargetReached)(w, r)
		w.Write([]byte("api"))
	})

	request := func(sni string, certificates []tls.Certificate) (*http.Response, error) {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         sni,
				Certificates:       certificates,
			},
		}
		t.Cleanup(transport.CloseIdleConnections)

		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://localhost:%d/api/items", server.HttpsPort()), nil)
		require.NoError(t, err)
		req.Host = "localhost"

		return (&http.Client{Transport: transport}).Do(req)
	}

	t.Run("rejects handshake without client certificate", func(t *testing.T) {
		_, err := request("localhost", nil)
		assert.Error(t, err)
	})

	t.Run("rejects request over connection for host without mTLS", func(t *testing.T) {
		resp, err := request("public.example.com", []tls.Certificate{ca.clientCert})
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode)
	})

	assert.False(t, pathTargetReached.Load())

	t.Run("accepts request with trusted client certificate", func(t *testing.T) {
		resp, err := request("localhost", []tls.Certificate{ca.clientCert})
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "api", string(body))
		assert.True(t, pathTargetReached.Load())
	})
}

func TestServer_ACMEALPNConnectionToClientCAHostNeverReachesTarget(t *testing.T) {
	ca := generateTestCA(t)
	var targetReached atomic.Bool
	target := testTarget(t, recordRequestsExceptHealthChecks(&targetReached))
	server := testServer(t, false)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"localhost"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.TLSClientCAPath = ca.certPath

	testDeployTarget(t, target, server, serviceOptions)

	for _, version := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
		t.Run(tls.VersionName(version), func(t *testing.T) {
			conn, err := tls.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpsPort()), &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         version,
				MaxVersion:         version,
				NextProtos:         []string{acme.ALPNProto},
			})
			require.NoError(t, err)
			defer conn.Close()
			require.Equal(t, acme.ALPNProto, conn.ConnectionState().NegotiatedProtocol)

			_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
			require.NoError(t, err)

			_, err = conn.Read(make([]byte, 1))
			assert.Error(t, err)
		})
	}

	assert.False(t, targetReached.Load())
}

func TestServer_ClientCAChangesApplyToOpenConnections(t *testing.T) {
	ca := generateTestCA(t)
	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server := testServer(t, false)

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.Hosts = []string{"localhost"}

	deployWithClientCA := func(clientCAPath string) {
		serviceOptions.TLSClientCAPath = clientCAPath
		testDeployTarget(t, target, server, serviceOptions)
	}

	var openedConnections int
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{ca.clientCert},
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			openedConnections++
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	requestOverOpenConnection := func() int {
		resp, err := testRequestUsingTransport(server, transport)
		require.NoError(t, err)
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)

		require.Equal(t, 1, openedConnections)
		return resp.StatusCode
	}

	deployWithClientCA(ca.certPath)
	assert.Equal(t, http.StatusOK, requestOverOpenConnection())

	deployWithClientCA(ca.certPath)
	assert.Equal(t, http.StatusOK, requestOverOpenConnection(), "redeploying with the same CA keeps open connections working")

	deployWithClientCA(generateTestCA(t).certPath)
	assert.Equal(t, http.StatusMisdirectedRequest, requestOverOpenConnection(), "replacing the CA rejects open connections verified by the old one")
}

func TestServer_PlainHTTPToClientCAHostNeverReachesTarget(t *testing.T) {
	ca := generateTestCA(t)
	server := testServer(t, false)
	certPath, keyPath := prepareTestCertificateFiles(t)

	var targetReached atomic.Bool
	deploy := func(name string, serviceOptions ServiceOptions) {
		target := testTarget(t, recordRequestsExceptHealthChecks(&targetReached))

		var result bool
		err := server.commandHandler.Deploy(DeployArgs{
			Service:           name,
			TargetURLs:        []string{target.Address()},
			DeploymentOptions: defaultDeploymentOptions,
			ServiceOptions:    serviceOptions,
			TargetOptions:     defaultTargetOptions,
		}, &result)
		require.NoError(t, err)
	}

	clientCAServiceOptions := defaultServiceOptions
	clientCAServiceOptions.Hosts = []string{"localhost"}
	clientCAServiceOptions.TLSEnabled = true
	clientCAServiceOptions.TLSCertificatePath = certPath
	clientCAServiceOptions.TLSPrivateKeyPath = keyPath
	clientCAServiceOptions.TLSClientCAPath = ca.certPath

	pathServiceOptions := defaultServiceOptions
	pathServiceOptions.Hosts = []string{"public.example.com", "localhost"}
	pathServiceOptions.PathPrefixes = []string{"/api"}

	deploy("mtls", clientCAServiceOptions)
	deploy("api", pathServiceOptions)

	request := func(path string) *http.Response {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Get(fmt.Sprintf("http://localhost:%d%s", server.HttpPort(), path))
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	t.Run("redirects to HTTPS", func(t *testing.T) {
		resp := request("/")
		assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		assert.Equal(t, "https://localhost/", resp.Header.Get("Location"))
	})

	t.Run("redirects to HTTPS for a path service that doesn't share the host's TLS options", func(t *testing.T) {
		resp := request("/api/items?page=2")
		assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		assert.Equal(t, "https://localhost/api/items?page=2", resp.Header.Get("Location"))
	})

	t.Run("rejects when TLS redirect is disabled", func(t *testing.T) {
		clientCAServiceOptions.TLSRedirect = false
		deploy("mtls", clientCAServiceOptions)

		assert.Equal(t, http.StatusForbidden, request("/").StatusCode)
		assert.Equal(t, http.StatusForbidden, request("/api/items").StatusCode)
	})

	assert.False(t, targetReached.Load())
}

func TestServer_PlainHTTPToClientCAHostServesACMEChallenges(t *testing.T) {
	ca := generateTestCA(t)
	var targetReached atomic.Bool
	target := testTarget(t, recordRequestsExceptHealthChecks(&targetReached))
	server := testServer(t, false)

	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"localhost"}
	serviceOptions.TLSEnabled = true
	serviceOptions.ACMECachePath = t.TempDir()
	serviceOptions.TLSClientCAPath = ca.certPath

	testDeployTarget(t, target, server, serviceOptions)

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost:%d/.well-known/acme-challenge/unknown-token", server.HttpPort()), nil)
	require.NoError(t, err)
	req.Host = "localhost"

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, string(body), "acme/autocert")
	assert.False(t, targetReached.Load())
}
