package server

import (
	"log/slog"
	"net/http"
	"time"
)

const (
	DefaultDeployTimeout = time.Second * 30
	DefaultDrainTimeout  = time.Second * 10
	DefaultPauseTimeout  = time.Second * 30

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckInterval = time.Second
	DefaultHealthCheckTimeout  = time.Second * 5

	MaxIdleConnsPerHost = 100

	DefaultTargetTimeout = time.Second * 10
)

type Service struct {
	name     string
	host     string
	active   *Target
	draining []*Target

	pauseControl *PauseControl
}

func NewService(name, host string) *Service {
	return &Service{
		name:         name,
		host:         host,
		pauseControl: NewPauseControl(),
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	proceed := s.pauseControl.Wait()
	if !proceed {
		slog.Warn("Rejecting request due to expired pause", "service", s.name, "path", r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	if s.active == nil {
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
		return
	}

	s.active.ServeHTTP(w, r)
}

func (s *Service) Pause(drainTimeout time.Duration, pauseTimeout time.Duration) error {
	err := s.pauseControl.Pause(pauseTimeout)
	if err != nil {
		return err
	}

	slog.Info("Service paused", "service", s.name)
	s.active.Drain(drainTimeout)
	slog.Info("Service drained", "service", s.name)
	return nil
}

func (s *Service) Resume() error {
	err := s.pauseControl.Resume()
	if err != nil {
		return err
	}

	slog.Info("Service resumed", "service", s.name)
	return nil
}
