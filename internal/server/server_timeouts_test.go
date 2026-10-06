package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go/http3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTimeout      = 200 * time.Millisecond
	testTimeoutLimit = 10 * testTimeout
)

func TestServer_ClosesConnectionWithStalledHeaders(t *testing.T) {
	server := testServerWithTimeouts(t, false)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n")

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesConnectionWithStalledTLSHandshake(t *testing.T) {
	server := testServerWithTimeouts(t, false)

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpsPort()))
	require.NoError(t, err)
	defer conn.Close()

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesIdleKeepAliveConnection(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	testDeployTarget(t, testTarget(t, func(w http.ResponseWriter, r *http.Request) {}), server, defaultServiceOptions)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")

	resp := readResponse(t, conn)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesStalledBodyForUnknownHost(t *testing.T) {
	server := testServerWithTimeouts(t, false)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: unknown.example.com\r\nContent-Length: 1000\r\n\r\nabc")

	resp := readResponse(t, conn)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesStalledBodyForBufferedTarget(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	deployEchoTarget(t, server, true)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 1000\r\n\r\nabc")

	resp := readResponse(t, conn)
	assert.Equal(t, http.StatusRequestTimeout, resp.StatusCode)

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesStalledBodyForUnbufferedTarget(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	deployEchoTarget(t, server, false)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 1000\r\n\r\nabc")

	resp := readResponse(t, conn)
	assert.Equal(t, http.StatusRequestTimeout, resp.StatusCode)

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesStalledBodyForRedirectedRequest(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	deployTLSEchoTarget(t, server)

	conn := dialHTTP(t, server)
	fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000\r\n\r\nabc")

	resp := readResponse(t, conn)
	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)

	assertConnectionClosedWithin(t, conn, testTimeoutLimit)
}

func TestServer_ClosesStalledBodyOverHTTP2(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	deployTLSEchoTarget(t, server)

	resp, err := postStalledBody(t, server, http2Transport())
	require.NoError(t, err)
	assert.Equal(t, "HTTP/2.0", resp.Proto)
	assert.Equal(t, http.StatusRequestTimeout, resp.StatusCode)
}

func TestServer_ClosesStalledBodyOverHTTP3(t *testing.T) {
	server := testServerWithTimeouts(t, true)
	deployTLSEchoTarget(t, server)

	resp, err := postStalledBody(t, server, http3Transport(t))
	require.NoError(t, err)
	assert.Equal(t, "HTTP/3.0", resp.Proto)
	assert.Equal(t, http.StatusRequestTimeout, resp.StatusCode)
}

func TestServer_AllowsSlowUploadThatMakesProgress(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		t.Run(fmt.Sprintf("buffered=%v", buffered), func(t *testing.T) {
			server := testServerWithTimeouts(t, false)
			deployEchoTarget(t, server, buffered)

			conn := dialHTTP(t, server)
			fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: example.com\r\nTransfer-Encoding: chunked\r\n\r\n")

			chunks := int(testTimeoutLimit / (testTimeout / 4))
			for range chunks {
				time.Sleep(testTimeout / 4)
				fmt.Fprint(conn, "5\r\nhello\r\n")
			}
			fmt.Fprint(conn, "0\r\n\r\n")

			resp := readResponse(t, conn)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, strconv.Itoa(chunks*5), readBody(t, resp))
		})
	}
}

func TestServer_AllowsSlowBackendResponse(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	testDeployTarget(t, testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * testTimeout)
	}), server, defaultServiceOptions)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/", server.HttpPort()))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_AllowsIdleWebSocket(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	testDeployTarget(t, testTarget(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			return
		}

		c, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		defer c.CloseNow()

		kind, message, err := c.Read(r.Context())
		require.NoError(t, err)
		require.NoError(t, c.Write(r.Context(), kind, message))
	}), server, defaultServiceOptions)

	ctx, cancel := context.WithTimeout(context.Background(), 5*testTimeoutLimit)
	defer cancel()

	c, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://localhost:%d/ws", server.HttpPort()), nil)
	require.NoError(t, err)
	defer c.CloseNow()

	time.Sleep(3 * testTimeout)

	require.NoError(t, c.Write(ctx, websocket.MessageText, []byte("ping")))
	_, message, err := c.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(message))
}

func TestServer_AllowsUploadThatWaitsOutAPause(t *testing.T) {
	server := testServerWithTimeouts(t, false)
	deployTLSEchoTarget(t, server)

	plainOptions := defaultServiceOptions
	plainOptions.Hosts = []string{"plain.example.com"}
	testDeployTargetAs(t, "plain", testTarget(t, echoBodyLengthHandler), server, plainOptions)

	pause := func(service string) {
		require.NoError(t, server.router.PauseService(service, DefaultDrainTimeout, 10*testTimeoutLimit))
		time.AfterFunc(3*testTimeout, func() {
			require.NoError(t, server.router.ResumeService(service))
		})
	}

	t.Run("http/1.1", func(t *testing.T) {
		pause("plain")

		conn := dialHTTP(t, server)
		fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: plain.example.com\r\nContent-Length: 5\r\n\r\nhello")

		resp := readResponse(t, conn)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "5", readBody(t, resp))
	})

	t.Run("http/2", func(t *testing.T) {
		pause("")

		client := &http.Client{Transport: http2Transport()}
		resp, err := client.Post(fmt.Sprintf("https://localhost:%d/", server.HttpsPort()), "text/plain", strings.NewReader("hello"))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, "HTTP/2.0", resp.Proto)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "5", readBody(t, resp))
	})
}

// Helpers

func testServerWithTimeouts(t testing.TB, http3Enabled bool) *Server {
	t.Helper()

	return testServerWithConfig(t, &Config{
		Bind:               "127.0.0.1",
		AlternateConfigDir: t.TempDir(),
		HTTP3Enabled:       http3Enabled,
		ReadHeaderTimeout:  testTimeout,
		IdleTimeout:        testTimeout,
		RequestBodyTimeout: testTimeout,
	})
}

func echoBodyLengthHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	fmt.Fprint(w, len(body))
}

func deployEchoTarget(t testing.TB, server *Server, buffered bool) {
	t.Helper()

	targetOptions := defaultTargetOptions
	targetOptions.BufferRequests = buffered
	targetOptions.MaxMemoryBufferSize = DefaultMaxMemoryBufferSize

	target := testTargetWithOptions(t, targetOptions, echoBodyLengthHandler)

	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		TargetURLs:        []string{target.Address()},
		DeploymentOptions: defaultDeploymentOptions,
		ServiceOptions:    defaultServiceOptions,
		TargetOptions:     targetOptions,
	}, &result)
	require.NoError(t, err)
}

func deployTLSEchoTarget(t *testing.T, server *Server) {
	t.Helper()

	certPath, keyPath := prepareTestCertificateFiles(t)
	serviceOptions := defaultServiceOptions
	serviceOptions.Hosts = []string{"localhost", "example.com"}
	serviceOptions.TLSEnabled = true
	serviceOptions.TLSCertificatePath = certPath
	serviceOptions.TLSPrivateKeyPath = keyPath
	serviceOptions.TLSRedirect = true

	testDeployTarget(t, testTarget(t, echoBodyLengthHandler), server, serviceOptions)
}

func dialHTTP(t testing.TB, server *Server) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", server.HttpPort()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return conn
}

func readResponse(t testing.TB, conn net.Conn) *http.Response {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(testTimeoutLimit)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	return resp
}

func readBody(t testing.TB, resp *http.Response) string {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func assertConnectionClosedWithin(t testing.TB, conn net.Conn, limit time.Duration) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(limit)))

	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err == nil {
			continue
		}

		if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
			t.Fatalf("connection still open after %v", limit)
		}
		return
	}
}

func postStalledBody(t *testing.T, server *Server, transport http.RoundTripper) (*http.Response, error) {
	t.Helper()

	reader, writer := io.Pipe()
	t.Cleanup(func() { writer.Close() })

	client := &http.Client{Transport: transport, Timeout: testTimeoutLimit}
	resp, err := client.Post(fmt.Sprintf("https://localhost:%d/", server.HttpsPort()), "text/plain", reader)
	if err == nil {
		t.Cleanup(func() { resp.Body.Close() })
	}
	return resp, err
}

func http2Transport() *http.Transport {
	return &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}
}

func http3Transport(t *testing.T) *http3.Transport {
	t.Helper()

	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		},
	}
	t.Cleanup(func() { _ = transport.Close() })

	return transport
}
