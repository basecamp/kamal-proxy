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
	ErrorHostInUse                   = errors.New("host is used by another service")
	ErrorNoServerName                = errors.New("no server name provided")
	ErrorUnknownServerName           = errors.New("unknown server name")
)

type Service struct {
	name     string
	host     string
	active   *Target
	adding   *Target
	draining []*Target
}

type ServiceMap map[string]*Service

type Router struct {
	statePath   string
	services    ServiceMap
	serviceLock sync.RWMutex
}

type ServiceDescription struct {
	Host   string `json:"host"`
	Target string `json:"target"`
}

type ServiceDescriptionMap map[string]ServiceDescription

func NewRouter(statePath string) *Router {
	return &Router{
		statePath: statePath,
		services:  ServiceMap{},
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

func (r *Router) SetServiceTarget(name string, host string, target *Target, deployTimeout time.Duration, drainTimeout time.Duration) error {
	slog.Info("Deploying", "service", name, "host", host, "target", target.Target(), "tls", target.options.RequireTLS())

	target.BeginHealthChecks()
	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	target.StopHealthChecks()

	if !becameHealthy {
		slog.Info("Target failed to become healthy", "host", host, "target", target.Target())
		return ErrorTargetFailedToBecomeHealthy
	}

	err := r.setActiveTarget(name, host, target, drainTimeout)
	if err != nil {
		return err
	}
	r.saveState()

	slog.Info("Deployed", "service", name, "host", host, "target", target.Target())
	return nil
}

func (r *Router) RemoveService(name string) error {
	err := r.withWriteLock(func() error {
		service := r.serviceForName(name)
		if service == nil {
			return ErrorServiceNotFound
		}

		if service.active != nil {
			r.drainAndDispose(service, service.active, DefaultDrainTimeout)
		}
		if service.adding != nil {
			service.adding.StopHealthChecks()
		}

		delete(r.services, service.host)
		return nil
	})

	r.saveState()

	return err
}

func (r *Router) PauseService(name string, drainTimeout time.Duration, pauseTimeout time.Duration) error {
	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.active.Pause(drainTimeout, pauseTimeout)
}

func (r *Router) ResumeService(name string) error {
	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.active.Resume()
}

func (r *Router) ListActiveServices() ServiceDescriptionMap {
	result := ServiceDescriptionMap{}

	r.withReadLock(func() error {
		for name, service := range r.services {
			var host string
			if service.host == "" {
				host = "*"
			} else {
				host = service.host
			}
			if service.active != nil {
				result[name] = ServiceDescription{
					Host:   host,
					Target: service.active.Target(),
				}
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

	target := r.activeTargetForHost(host)
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
	Host              string            `json:"host"`
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
		slog.Error("Unable to save state", "error", err, "path", r.statePath)
		return err
	}

	slog.Debug("Saved state", "path", r.statePath)
	return nil
}

func (r *Router) restoreSnapshot(state savedState) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	r.services = ServiceMap{}
	for name, saved := range state.ActiveTargets {
		target, err := NewTarget(saved.Target, saved.HealthCheckConfig, saved.TargetOptions)
		if err != nil {
			return err
		}

		// Put the target back into the active state, regardless of its health. It
		// may be rebooting or otherwise need more time to be reachable again.
		target.state = TargetStateHealthy
		r.services[saved.Host] = &Service{
			name:   name,
			host:   saved.Host,
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
		for _, service := range r.services {
			if service.active != nil {
				state.ActiveTargets[service.name] = savedTarget{
					Host:              service.host,
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

func (r *Router) setActiveTarget(name string, host string, target *Target, drainTimeout time.Duration) error {
	target.StopHealthChecks()

	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	service := r.serviceForName(name)
	if service == nil {
		service = &Service{name: name, host: host}
	}

	hostService, ok := r.services[host]
	if !ok {
		if host != service.host {
			delete(r.services, service.host)
			service.host = host
		}

		r.services[host] = service
	} else if hostService != service {
		slog.Error("Host in use by another service", "service", hostService.name, "host", host)
		return ErrorHostInUse
	}

	if service.active != nil {
		r.drainAndDispose(service, service.active, drainTimeout)
	}

	service.active = target

	return nil
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

func (r *Router) serviceForName(name string) *Service {
	for _, service := range r.services {
		if name == service.name {
			return service
		}
	}

	return nil
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
