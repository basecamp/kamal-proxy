package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/basecamp/kamal-proxy/internal/pages"
)

const (
	ACMEStagingDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"

	shutdownTimeout = 10 * time.Second
)

type Server struct {
	config         *Config
	router         *Router
	httpListener   net.Listener
	httpsListener  net.Listener
	httpServer     *http.Server
	httpsServer    *http.Server
	commandHandler *CommandHandler
}

func NewServer(config *Config, router *Router) *Server {
	return &Server{
		config: config,
		router: router,
	}
}

func (s *Server) Start() error {
	err := s.startHTTPServers()
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

	s.commandHandler.Close()
	s.httpServer.Shutdown(ctx)

	slog.Info("Server stopped")
}

func (s *Server) HttpPort() int {
	return s.httpListener.Addr().(*net.TCPAddr).Port
}

func (s *Server) HttpsPort() int {
	return s.httpsListener.Addr().(*net.TCPAddr).Port
}

// Private

func (s *Server) startHTTPServers() error {
	httpAddr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.HttpPort)
	httpsAddr := fmt.Sprintf("%s:%d", s.config.Bind, s.config.HttpsPort)

	handler := s.buildHandler()

	l, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return err
	}
	s.httpListener = l
	s.httpServer = &http.Server{
		Addr:    httpAddr,
		Handler: handler,
	}

	l, err = net.Listen("tcp", httpsAddr)
	if err != nil {
		return err
	}
	s.httpsListener = l
	s.httpsServer = &http.Server{
		Addr:    httpsAddr,
		Handler: handler,
		TLSConfig: &tls.Config{
			NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
			GetCertificate: s.router.GetCertificate,
		},
	}

	go s.httpServer.Serve(s.httpListener)
	go s.httpsServer.ServeTLS(s.httpsListener, "", "")

	return nil
}

func (s *Server) startCommandHandler() error {
	s.commandHandler = NewCommandHandler(s.router)
	_ = os.Remove(s.config.SocketPath())

	return s.commandHandler.Start(s.config.SocketPath())
}

func (s *Server) buildHandler() http.Handler {
	var handler http.Handler

	handler = s.router
	handler = WithErrorPageMiddleware(pages.DefaultErrorPages, true, handler)
	handler = WithLoggingMiddleware(slog.Default(), s.config.HttpPort, s.config.HttpsPort, handler)
	handler = WithRequestIDMiddleware(handler)

	return handler
}
