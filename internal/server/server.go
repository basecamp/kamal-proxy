package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/quic-go/quic-go/http3"
	"golang.org/x/crypto/acme"

	"github.com/basecamp/kamal-proxy/internal/metrics"
	"github.com/basecamp/kamal-proxy/internal/pages"
)

const (
	ACMEStagingDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

	shutdownTimeout = 10 * time.Second
)

type Server struct {
	config          *Config
	router          *Router
	httpListener    net.Listener
	httpsListener   net.Listener
	http3Listener   net.PacketConn
	metricsListener net.Listener
	httpServer      *http.Server
	httpsServer     *http.Server
	http3Server     *http3.Server
	metricsServer   *http.Server
	commandHandler  *CommandHandler
}

func NewServer(config *Config, router *Router) *Server {
	return &Server{
		config: config,
		router: router,
	}
}

func (s *Server) Start() error {
	err := s.startMetricsServer()
	if err != nil {
		return err
	}

	err = s.startHTTPServers()
	if err != nil {
		return err
	}

	err = s.startCommandHandler()
	if err != nil {
		return err
	}

	slog.Info("Server started", "http", s.HttpPort(), "https", s.HttpsPort())
	return nil
}

func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	PerformConcurrently(
		func() { _ = s.commandHandler.Close() },
		func() { s.stopHTTPServer(ctx, s.httpServer) },
		func() { s.stopHTTPServer(ctx, s.httpsServer) },
		func() {
			if s.http3Server != nil {
				s.stopHTTPServer(ctx, s.http3Server)
			}
		},
		func() {
			if s.metricsServer != nil {
				s.stopHTTPServer(ctx, s.metricsServer)
			}
		},
	)

	s.httpListener.Close()
	s.httpsListener.Close()

	if s.http3Listener != nil {
		s.http3Listener.Close()
	}
	if s.metricsListener != nil {
		s.metricsListener.Close()
	}

	slog.Info("Server stopped")
}

func (s *Server) HttpPort() int {
	return s.httpListener.Addr().(*net.TCPAddr).Port
}

func (s *Server) HttpsPort() int {
	return s.httpsListener.Addr().(*net.TCPAddr).Port
}

// Private

func (s *Server) startHTTP3Server(handler http.Handler, httpsAddr string) error {
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")

	http3Listener, err := net.ListenPacket("udp", httpsAddr)
	if err != nil {
		return err
	}

	s.http3Listener = http3Listener
	s.http3Server = &http3.Server{
		Handler: handler,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS13,
			NextProtos:     []string{"h3"},
			GetCertificate: s.router.GetCertificate,
		},
	}

	go s.http3Server.Serve(s.http3Listener)
	return nil
}

func (s *Server) startHTTPServers() error {
	httpAddr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.HttpPort)
	httpsAddr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.HttpsPort)

	handler := s.buildHandler()

	httpListener, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return err
	}
	s.httpListener = httpListener
	s.httpServer = &http.Server{
		Handler: handler,
	}

	httpsListener, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		return err
	}
	s.httpsListener = httpsListener
	s.httpsServer = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.config.HTTP3Enabled {
				s.http3Server.SetQUICHeaders(w.Header())
			}

			handler.ServeHTTP(w, r)
		}),
		TLSConfig: httpsTLSConfig(s.router.GetCertificate),
	}

	go s.httpServer.Serve(s.httpListener)
	go s.httpsServer.ServeTLS(s.httpsListener, "", "")

	if s.config.HTTP3Enabled {
		http3Addr := httpsListener.Addr().String()
		err = s.startHTTP3Server(handler, http3Addr)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) startMetricsServer() error {
	if s.config.MetricsPort == 0 {
		slog.Debug("Metrics server disabled")
		return nil
	}

	addr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.MetricsPort)
	handler := metrics.Enable()

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.metricsListener = l
	s.metricsServer = &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go s.metricsServer.Serve(s.metricsListener)

	slog.Info("Metrics enabled", "address", addr)

	return nil
}

func (s *Server) startCommandHandler() error {
	s.commandHandler = NewCommandHandler(s.router)
	_ = os.Remove(s.config.SocketPath())

	return s.commandHandler.Start(s.config.SocketPath())
}

func (s *Server) buildHandler() http.Handler {
	var handler http.Handler

	// Note: handlers are executed in the inverse order.
	handler = s.router
	handler, _ = WithErrorPageMiddleware(pages.DefaultErrorPages, true, handler)
	handler = WithLoggingMiddleware(slog.Default(), s.config.HttpPort, s.config.HttpsPort, handler)
	handler = WithRequestIDMiddleware(handler)
	handler = WithRequestStartMiddleware(handler)

	return handler
}

type shutdownable interface {
	Shutdown(ctx context.Context) error
}

func (s *Server) stopHTTPServer(ctx context.Context, server shutdownable) {
	if server != nil {
		err := server.Shutdown(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Warn("Closing active connections")
			} else {
				slog.Error("Error while attempting to stop server", "error", err)
			}
		}
	}
}

// httpsTLSConfig builds the TLS configuration for the HTTPS listener.
//
// Go's default TLS 1.2 cipher suite list still includes the CBC-mode suites
// (TLS_ECDHE_*_WITH_AES_*_CBC_SHA), which external scanners flag as obsolete
// (BEAST / Lucky13 class). Pin the TLS 1.2 suites to the AEAD set (AES-GCM and
// ChaCha20-Poly1305 over ECDHE, for both ECDSA and RSA certificates) and the
// minimum version to TLS 1.2. TLS 1.3 suites are not configurable and are
// unaffected, as is the ACME TLS-ALPN-01 challenge.
func httpsTLSConfig(getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		CipherSuites:   aeadCipherSuites,
		NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
		GetCertificate: getCertificate,
	}
}

var aeadCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}
