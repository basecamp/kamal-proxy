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

type Service struct {
	active   *Target
	adding   *Target
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
			slog.Debug("No state to restore", "event.dataset", "proxy.state.restore", "file.path", r.statePath)
			return nil
		}
		slog.Error("Failed to restore saved state", "event.dataset", "proxy.state.restore", "file.path", r.statePath, "error.message", err)
		return err
	}

	var state savedState
	err = json.NewDecoder(f).Decode(&state)
	if err != nil {
		slog.Error("Failed to decode saved state", "event.dataset", "proxy.state.restore", "file.path", r.statePath, "error.message", err)
		return err
	}

	slog.Info("Restoring saved state", "event.dataset", "proxy.state.restore", "file.path", r.statePath)
	return r.restoreSnapshot(state)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target := r.activeTargetForRequest(req)
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

func (r *Router) SetServiceTarget(host string, target *Target, deployTimeout time.Duration) error {
	slog.Info("Deploying", "event.dataset", "proxy.deployments", "source.domain", host, "destination.address", target.Target(), "tls.enabled", target.options.RequireTLS())

	service := r.setAddingService(host, target)

	target.BeginHealthChecks()
	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	if !becameHealthy {
		slog.Info("Target failed to become healthy", "event.dataset", "proxy.deployments", "event.outcome", "failure", "source.domain", host, "destination.address", target.Target())
		r.setAddingService(host, nil)
		return ErrorTargetFailedToBecomeHealthy
	}

	r.promoteToActive(service, target)
	r.saveState()

	slog.Info("Deployed", "event.dataset", "proxy.deployments", "event.outcome", "success", "source.domain", host, "destination.address", target.Target(), "tls.enabled", target.options.RequireTLS())
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
		if service.adding != nil {
			service.adding.StopHealthChecks()
		}

		delete(r.services, host)
		return nil
	})

	r.saveState()

	return err
}

func (r *Router) ListActiveServices() map[string]string {
	result := map[string]string{}

	r.withReadLock(func() error {
		for host, service := range r.services {
			if host == "" {
				host = "*"
			}
			if service.active != nil {
				result[host] = service.active.targetURL.Host
			}
		}
		return nil
	})

	return result
}

func (r *Router) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := hello.ServerName
	if host == "" {
		slog.Debug("Unable to get certificate", "event.dataset", "proxy.tls", "event.outcome", "failure", "source.domain", host, "error.message", "no server name")
		return nil, ErrorNoServerName
	}

	target := r.activeTargetForHost(host)
	if target == nil {
		slog.Debug("Unable to get certificate", "event.dataset", "proxy.tls", "event.outcome", "failure", "source.domain", host, "error.message", "unknown server name")
		return nil, ErrorUnknownServerName
	}

	if target.certManager == nil {
		slog.Debug("Unable to get certificate", "event.dataset", "proxy.tls", "event.outcome", "failure", "source.domain", host, "error.message", "target does not support TLS")
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

type savedState struct {
	ActiveTargets map[string]savedTarget `json:"active_targets"`
}

func (r *Router) saveState() error {
	state := r.snaphostState()

	f, err := os.Create(r.statePath)
	if err != nil {
		return err
	}

	err = json.NewEncoder(f).Encode(state)
	if err != nil {
		slog.Error("Failed to save state", "event.dataset", "proxy.state.save", "file.path", r.statePath, "error.message", err)
		return err
	}

	slog.Debug("Saved state", "event.dataset", "proxy.state.save", "file.path", r.statePath)
	return nil
}

func (r *Router) restoreSnapshot(state savedState) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	r.services = HostServiceMap{}
	for host, saved := range state.ActiveTargets {
		target, err := NewTarget(saved.Target, saved.HealthCheckConfig, saved.TargetOptions)
		if err != nil {
			return err
		}

		// Put the target back into the active state, regardless of its health. It
		// may be rebooting or otherwise need more time to be reachable again.
		target.state = TargetStateHealthy
		r.services[host] = &Service{
			active: target,
		}
	}

	return nil
}

func (r *Router) snaphostState() savedState {
	state := savedState{
		ActiveTargets: map[string]savedTarget{},
	}

	r.withReadLock(func() error {
		for host, service := range r.services {
			if service.active != nil {
				state.ActiveTargets[host] = savedTarget{
					Target:            service.active.Target(),
					HealthCheckConfig: service.active.healthCheckConfig,
					TargetOptions:     service.active.options,
				}
			}
		}
		return nil
	})

	return state
}

func (r *Router) activeTargetForRequest(req *http.Request) *Target {
	return r.activeTargetForHost(req.Host)
}

func (r *Router) activeTargetForHost(host string) *Target {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service, ok := r.services[host]
	if !ok {
		service, ok = r.services[""]
	}

	if !ok {
		return nil
	}

	return service.active
}

func (r *Router) setAddingService(host string, target *Target) *Service {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	service, ok := r.services[host]
	if !ok {
		service = &Service{}
		r.services[host] = service
	}

	if service.adding != nil {
		r.drainAndDispose(service, service.adding, DefaultDrainTimeout)
	}

	service.adding = target
	return service
}

func (r *Router) promoteToActive(service *Service, target *Target) {
	target.StopHealthChecks()

	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	if service.active != nil {
		r.drainAndDispose(service, service.active, DefaultDrainTimeout)
	}

	service.active = target
	service.adding = nil
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
