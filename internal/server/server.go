package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Server struct {
	config         *Config
	router         *Router
	httpServer     *http.Server
	commandHandler *CommandHandler
}

func NewServer(config *Config, router *Router) *Server {
	return &Server{
		config: config,
		router: router,
	}
}

func (s *Server) Start() {
	addr := fmt.Sprintf(":%d", s.config.HttpPort)
	handler := s.buildHandler()

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		IdleTimeout:  s.config.HttpIdleTimeout,
		ReadTimeout:  s.config.HttpReadTimeout,
		WriteTimeout: s.config.HttpWriteTimeout,
	}

	s.commandHandler = NewCommandHandler(s.router)

	go s.httpServer.ListenAndServe()
	go s.commandHandler.Start(s.config.SocketPath())

	slog.Info("Server started", "http", s.config.HttpPort)
}

func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.commandHandler.Stop()
	s.httpServer.Shutdown(ctx)

	slog.Info("Server stopped")
}

// Private

func (s *Server) buildHandler() http.Handler {
	var handler http.Handler = s.router
	if s.config.MaxRequestBodySize > 0 {
		handler = http.MaxBytesHandler(handler, int64(s.config.MaxRequestBodySize))
	}

	handler = NewLoggingMiddleware(slog.Default(), handler)
	return handler
}
