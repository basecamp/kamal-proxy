package server

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	ErrorServiceNotFound             = errors.New("service not found")
	ErrorTargetFailedToBecomeHealthy = errors.New("target failed to become healthy")
	ErrorNoServerName                = errors.New("no server name provided")
	ErrorUnknownServerName           = errors.New("unknown server name")
)

type TargetSlot int

const (
	TargetSlotActive TargetSlot = iota
	TargetSlotRollout
)

type Service struct {
	active   *Target
	rollout  *Target
	draining []*Target
}

type HostServiceMap map[string]*Service

type Router struct {
	statePath   string
	services    HostServiceMap
	serviceLock sync.RWMutex
}

func NewRouter(statePath string) *Router {
	return &Router{
		statePath: statePath,
		services:  HostServiceMap{},
	}
}

func (r *Router) RestoreLastSavedState() error {
	f, err := os.Open(r.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("No state to restore", "path", r.statePath)
			return nil
		}
		slog.Error("Failed to restore saved state", "path", r.statePath, "error", err)
		return err
	}

	var state savedState
	err = json.NewDecoder(f).Decode(&state)
	if err != nil {
		slog.Error("Failed to decode saved state", "path", r.statePath, "error", err)
		return err
	}

	slog.Info("Restoring saved state", "path", r.statePath)
	return r.restoreSnapshot(state)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target := r.targetForRequest(req)
	if target == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		// Record the target that served the request, if its context is available.
		targetIdentifer, ok := req.Context().Value(contextKeyTarget).(*string)
		if ok {
			*targetIdentifer = target.Target()
		}

		target.ServeHTTP(w, req)
	}
}

func (r *Router) SetServiceTarget(host string, target *Target, deployTimeout time.Duration, drainTimeout time.Duration) error {
	slog.Info("Deploying", "host", host, "target", target.Target(), "tls", target.options.RequireTLS())

	target.BeginHealthChecks()
	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	target.StopHealthChecks()

	if !becameHealthy {
		slog.Info("Target failed to become healthy", "host", host, "target", target.Target())
		return ErrorTargetFailedToBecomeHealthy
	}

	r.setTarget(host, TargetSlotActive, target, drainTimeout)
	r.saveState()

	slog.Info("Deployed", "host", host, "target", target.Target())
	return nil
}

func (r *Router) RemoveService(host string) error {
	err := r.withWriteLock(func() error {
		service, ok := r.services[host]
		if !ok {
			return ErrorServiceNotFound
		}

		if service.active != nil {
			r.drainAndDispose(service, service.active, DefaultDrainTimeout)
		}

		delete(r.services, host)
		return nil
	})

	r.saveState()

	return err
}

func (r *Router) PauseService(host string, drainTimeout time.Duration, pauseTimeout time.Duration) error {
	target := r.targetForHost(host, TargetSlotActive)
	if target == nil {
		return ErrorServiceNotFound
	}

	return target.Pause(drainTimeout, pauseTimeout)
}

func (r *Router) ResumeService(host string) error {
	target := r.targetForHost(host, TargetSlotActive)
	if target == nil {
		return ErrorServiceNotFound
	}

	return target.Resume()
}

func (r *Router) ListActiveServices() map[string]string {
	result := map[string]string{}

	r.withReadLock(func() error {
		for host, service := range r.services {
			if host == "" {
				host = "*"
			}
			if service.active != nil {
				result[host] = service.active.Target()
			}
		}
		return nil
	})

	return result
}

func (r *Router) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := hello.ServerName
	if host == "" {
		slog.Debug("ACME: Unable to get certificate (no server name)")
		return nil, ErrorNoServerName
	}

	target := r.targetForHost(host, TargetSlotActive)
	if target == nil {
		slog.Debug("ACME: Unable to get certificate (unknown server name)")
		return nil, ErrorUnknownServerName
	}

	if target.certManager == nil {
		slog.Debug("ACME: Unable to get certificate (target does not support TLS)")
		return nil, ErrorUnknownServerName
	}

	return target.certManager.GetCertificate(hello)
}

// Private

type savedTarget struct {
	Target            string            `json:"target"`
	HealthCheckConfig HealthCheckConfig `json:"health_check_config"`
	TargetOptions     TargetOptions     `json:"target_options"`
}

type savedService struct {
	Active  savedTarget `json:"active"`
	Rollout savedTarget `json:"rollout"`
}

type savedState struct {
	Services map[string]savedService `json:"targets"`
}

func (r *Router) saveState() error {
	state := r.snaphostState()

	f, err := os.Create(r.statePath)
	if err != nil {
		return err
	}

	err = json.NewEncoder(f).Encode(state)
	if err != nil {
		slog.Error("Unable to save state", "error", err, "path", r.statePath)
		return err
	}

	slog.Debug("Saved state", "path", r.statePath)
	return nil
}

func (r *Router) restoreSnapshot(state savedState) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	r.services = HostServiceMap{}
	for host, savedService := range state.Services {
		service := &Service{}
		if savedService.Active.Target != "" {
			target, err := r.restoreTarget(savedService.Active)
			if err != nil {
				return err
			}
			service.active = target
		}

		if savedService.Rollout.Target != "" {
			target, err := r.restoreTarget(savedService.Rollout)
			if err != nil {
				return err
			}
			service.rollout = target
		}

		r.services[host] = service
	}

	return nil
}

func (r *Router) restoreTarget(saved savedTarget) (*Target, error) {
	target, err := NewTarget(saved.Target, saved.HealthCheckConfig, saved.TargetOptions)
	if err != nil {
		return nil, err
	}

	// Put the target back into the active state, regardless of its health. It
	// may be rebooting or otherwise need more time to be reachable again.
	target.state = TargetStateHealthy
	return target, nil
}

func (r *Router) snaphostState() savedState {
	state := savedState{
		Services: map[string]savedService{},
	}

	r.withReadLock(func() error {
		for host, service := range r.services {
			savedService := savedService{}
			if service.active != nil {
				savedService.Active = savedTarget{
					Target:            service.active.Target(),
					HealthCheckConfig: service.active.healthCheckConfig,
					TargetOptions:     service.active.options,
				}
			}
			if service.rollout != nil {
				savedService.Rollout = savedTarget{
					Target:            service.rollout.Target(),
					HealthCheckConfig: service.rollout.healthCheckConfig,
					TargetOptions:     service.rollout.options,
				}
			}
			state.Services[host] = savedService
		}

		return nil
	})

	return state
}

func (r *Router) targetForRequest(req *http.Request) *Target {
	slot := TargetSlotActive
	return r.targetForHost(req.Host, slot)
}

func (r *Router) targetForHost(host string, slot TargetSlot) *Target {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service, ok := r.services[host]
	if !ok {
		service, ok = r.services[""]
	}

	if !ok {
		return nil
	}

	if slot == TargetSlotRollout {
		if service.rollout != nil {
			return service.rollout
		}
		slog.Warn("Rollout is enabled but no rollout host is available; falling back to active host instead", "host", host)
	}

	return service.active
}

func (r *Router) setTarget(host string, slot TargetSlot, target *Target, drainTimeout time.Duration) {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	service, ok := r.services[host]
	if !ok {
		service = &Service{}
		r.services[host] = service
	}

	if slot == TargetSlotRollout {
		if service.rollout != nil {
			r.drainAndDispose(service, service.rollout, drainTimeout)
		}
		service.rollout = target
	} else {
		if service.active != nil {
			r.drainAndDispose(service, service.active, drainTimeout)
		}
		service.active = target
	}
}

func (r *Router) drainAndDispose(service *Service, target *Target, drainTimeout time.Duration) {
	target.StopHealthChecks()
	service.draining = append(service.draining, target)

	go func() {
		target.Drain(drainTimeout)

		r.withWriteLock(func() error {
			service.draining = removeItem(service.draining, target)
			return nil
		})
	}()
}

func removeItem[T comparable](s []T, item T) []T {
	for i, v := range s {
		if v == item {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func (r *Router) withReadLock(fn func() error) error {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return fn()
}

func (r *Router) withWriteLock(fn func() error) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	return fn()
}
