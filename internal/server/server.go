package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/acme"
)

const (
	ACMEStagingDirectoryURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

var (
	ErrorHostTLSNotPermitted = errors.New("host not permitted for TLS")
)

type Server struct {
	config         *Config
	router         *Router
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

func (s *Server) Start() {
	s.startHTTPServers()
	s.startCommandHandler()

	slog.Info("Server started", "http", s.config.HttpPort, "https", s.config.HttpsPort)
}

func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.commandHandler.Stop()
	s.httpServer.Shutdown(ctx)

	slog.Info("Server stopped")
}

// Private

func (s *Server) startHTTPServers() {
	httpAddr := fmt.Sprintf(":%d", s.config.HttpPort)
	httpsAddr := fmt.Sprintf(":%d", s.config.HttpsPort)

	handler := s.buildHandler()

	s.httpServer = &http.Server{
		Addr:         httpAddr,
		Handler:      handler,
		IdleTimeout:  s.config.HttpIdleTimeout,
		ReadTimeout:  s.config.HttpReadTimeout,
		WriteTimeout: s.config.HttpWriteTimeout,
	}

	s.httpsServer = &http.Server{
		Addr:         httpsAddr,
		Handler:      handler,
		IdleTimeout:  s.config.HttpIdleTimeout,
		ReadTimeout:  s.config.HttpReadTimeout,
		WriteTimeout: s.config.HttpWriteTimeout,
		TLSConfig: &tls.Config{
			NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
			GetCertificate: s.router.GetCertificate,
		},
	}

	go s.httpServer.ListenAndServe()
	go s.httpsServer.ListenAndServeTLS("", "")
}

func (s *Server) startCommandHandler() {
	s.commandHandler = NewCommandHandler(s.router)

	go s.commandHandler.Start(s.config.SocketPath())
}

func (s *Server) buildHandler() http.Handler {
	return NewLoggingMiddleware(slog.Default(), s.router)
}
