package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	shutdownTimeout = time.Second * 10
)

type Server struct {
	config         Config
	httpServer     *http.Server
	httpListener   net.Listener
	loadBalancer   *LoadBalancer
	commandHandler *CommandHandler
}

func NewServer(c Config) *Server {
	server := &Server{
		config:       c,
		loadBalancer: NewLoadBalancer(c),
	}

	server.commandHandler = NewCommandHandler(server.loadBalancer)
	server.setLogger()

	return server
}

func (s *Server) LoadBalancer() *LoadBalancer {
	return s.loadBalancer
}

func (s *Server) Addr() string {
	if s.httpListener == nil {
		return ""
	}
	return s.httpListener.Addr().String()
}

func (s *Server) Start() error {
	slog.Info("Server starting")

	err := s.loadBalancer.RestoreFromStateFile()
	if err != nil {
		return err
	}

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.ListenPort),
		Handler: s.addMiddleware(),

		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	s.httpListener, err = net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	go s.httpServer.Serve(s.httpListener)

	err = s.commandHandler.Start(s.config.SocketPath())
	if err != nil {
		return err
	}

	slog.Info("Server started")
	return nil
}

func (s *Server) Stop() error {
	slog.Info("Server stopping")
	defer slog.Info("Server stopped")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := s.httpServer.Shutdown(ctx)
	if err != nil {
		return err
	}
	s.httpServer = nil
	s.httpListener = nil

	return s.commandHandler.Stop()
}

// Private

func (s *Server) addMiddleware() http.Handler {
	var handler http.Handler = s.loadBalancer

	if s.config.MaxRequestBodySize > 0 {
		handler = http.MaxBytesHandler(handler, s.config.MaxRequestBodySize)
	}

	handler = NewLoggingMiddleware(slog.Default(), handler)

	return handler
}

func (s *Server) setLogger() {
	level := slog.LevelInfo
	if s.config.Debug {
		level = slog.LevelDebug
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
