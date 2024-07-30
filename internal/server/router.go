package server

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
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

type HostServiceMap map[string]*Service

type Router struct {
	statePath   string
	services    HostServiceMap
	serviceLock sync.RWMutex
}

type ServiceDescription struct {
	Host   string `json:"host"`
	TLS    bool   `json:"tls"`
	Target string `json:"target"`
	State  string `json:"state"`
}

type ServiceDescriptionMap map[string]ServiceDescription

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

	var services []*Service
	err = json.NewDecoder(f).Decode(&services)
	if err != nil {
		slog.Error("Failed to decode saved state", "path", r.statePath, "error", err)
		return err
	}

	r.withWriteLock(func() error {
		r.services = HostServiceMap{}
		for _, service := range services {
			r.services[service.host] = service
		}
		return nil
	})

	slog.Info("Restored saved state", "path", r.statePath)
	return nil
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	service := r.serviceForRequest(req)
	if service == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	service.ServeHTTP(w, req)
}

func (r *Router) SetServiceTarget(name string, host string, targetURL string,
	options ServiceOptions, targetOptions TargetOptions,
	deployTimeout time.Duration, drainTimeout time.Duration) error {

	slog.Info("Deploying", "service", name, "host", host, "target", targetURL, "tls", options.RequireTLS())

	target, err := NewTarget(targetURL, targetOptions)
	if err != nil {
		return err
	}

	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	if !becameHealthy {
		slog.Info("Target failed to become healthy", "host", host, "target", targetURL)
		return ErrorTargetFailedToBecomeHealthy
	}

	err = r.setActiveTarget(name, host, target, options, drainTimeout)
	if err != nil {
		return err
	}

	slog.Info("Deployed", "service", name, "host", host, "target", targetURL)

	return r.saveStateSnapshot()
}

func (r *Router) SetRolloutTarget(name string, targetURL string, deployTimeout time.Duration, drainTimeout time.Duration) error {
	slog.Info("Deploying for rollout", "service", name, "target", targetURL)

	// TODO: check locking here, since our ordering is different from the usual case.

	service := r.serviceForName(name, true)
	if service == nil {
		return ErrorServiceNotFound
	}
	targetOptions := service.ActiveTarget().options

	target, err := NewTarget(targetURL, targetOptions)
	if err != nil {
		return err
	}

	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	if !becameHealthy {
		slog.Info("Rollout target failed to become healthy", "service", service, "target", targetURL)
		return ErrorTargetFailedToBecomeHealthy
	}

	service.SetTarget(TargetSlotRollout, target, drainTimeout)

	slog.Info("Deployed for rollout", "service", name, "target", targetURL)

	return r.saveStateSnapshot()
}

func (r *Router) RemoveService(name string) error {
	err := r.withWriteLock(func() error {
		service := r.serviceForName(name, false)
		if service == nil {
			return ErrorServiceNotFound
		}

		service.SetTarget(TargetSlotActive, nil, DefaultDrainTimeout)
		delete(r.services, service.host)

		return nil
	})

	if err != nil {
		return err
	}

	return r.saveStateSnapshot()
}

func (r *Router) PauseService(name string, drainTimeout time.Duration, pauseTimeout time.Duration) error {
	service := r.serviceForName(name, true)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Pause(drainTimeout, pauseTimeout)
}

func (r *Router) StopService(name string, drainTimeout time.Duration) error {
	service := r.serviceForName(name, true)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Stop(drainTimeout)
}

func (r *Router) ResumeService(name string) error {
	service := r.serviceForName(name, true)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Resume()
}

func (r *Router) ListActiveServices() ServiceDescriptionMap {
	result := ServiceDescriptionMap{}

	r.withReadLock(func() error {
		for host, service := range r.services {
			if host == "" {
				host = "*"
			}
			if service.active != nil {
				result[service.name] = ServiceDescription{
					Host:   host,
					Target: service.active.Target(),
					TLS:    service.options.RequireTLS(),
					State:  service.pauseControl.State().String(),
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

	service := r.serviceForHost(host)
	if service == nil {
		slog.Debug("ACME: Unable to get certificate (unknown server name)")
		return nil, ErrorUnknownServerName
	}

	if service.certManager == nil {
		slog.Debug("ACME: Unable to get certificate (service does not support TLS)")
		return nil, ErrorUnknownServerName
	}

	return service.certManager.GetCertificate(hello)
}

// Private

func (r *Router) saveStateSnapshot() error {
	services := []*Service{}
	r.withReadLock(func() error {
		for _, service := range r.services {
			services = append(services, service)
		}
		return nil
	})

	f, err := os.Create(r.statePath)
	if err != nil {
		return err
	}

	err = json.NewEncoder(f).Encode(services)
	if err != nil {
		slog.Error("Unable to save state", "error", err, "path", r.statePath)
		return err
	}

	slog.Debug("Saved state", "path", r.statePath)
	return nil
}

func (r *Router) serviceForRequest(req *http.Request) *Service {
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}

	return r.serviceForHost(host)
}

func (r *Router) serviceForHost(host string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service, ok := r.services[host]
	if !ok {
		service = r.services[""]
	}

	return service
}

func (r *Router) setActiveTarget(name string, host string, target *Target, options ServiceOptions, drainTimeout time.Duration) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	service := r.serviceForName(name, false)
	if service == nil {
		service = NewService(name, host, options)
	} else {
		service.UpdateOptions(options)
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

	service.SetTarget(TargetSlotActive, target, drainTimeout)

	return nil
}

func (r *Router) serviceForName(name string, readLock bool) *Service {
	if readLock {
		r.serviceLock.RLock()
		defer r.serviceLock.RUnlock()
	}

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
