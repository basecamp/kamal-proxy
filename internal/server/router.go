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

type ServiceMap map[string]*Service
type HostServiceMap map[string]*Service

func (m *ServiceMap) HostServices() HostServiceMap {
	hostServices := HostServiceMap{}
	for _, service := range *m {
		hostServices[service.host] = service
	}
	return hostServices
}

func (m *ServiceMap) CheckHostAvailability(service *Service, host string) *Service {
	for _, s := range *m {
		if s.host == host && s != service {
			return s
		}
	}
	return nil
}

type Router struct {
	statePath    string
	services     ServiceMap
	hostServices HostServiceMap
	serviceLock  sync.RWMutex
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
		statePath:    statePath,
		services:     ServiceMap{},
		hostServices: HostServiceMap{},
	}
}

func (r *Router) RestoreLastSavedState() error {
	f, err := os.Open(r.statePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("No previous state to restore", "path", r.statePath)
			return nil
		}
		slog.Error("Failed to restore saved state", "path", r.statePath, "error", err)
		return err
	}
	defer f.Close()

	var services []*Service
	err = json.NewDecoder(f).Decode(&services)
	if err != nil {
		slog.Error("Failed to decode saved state", "path", r.statePath, "error", err)
		return err
	}

	r.withWriteLock(func() error {
		r.services = ServiceMap{}
		for _, service := range services {
			r.services[service.name] = service
		}

		r.hostServices = r.services.HostServices()
		return nil
	})

	slog.Info("Restored saved state", "path", r.statePath)
	return nil
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	service := r.serviceForRequest(req)
	if service == nil {
		SetErrorResponse(w, req, http.StatusNotFound, nil)
		return
	}

	service.ServeHTTP(w, req)
}

func (r *Router) SetServiceTarget(name string, host string, targetURL string,
	options ServiceOptions, targetOptions TargetOptions,
	deployTimeout time.Duration, drainTimeout time.Duration,
) error {
	defer r.saveStateSnapshot()

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
	return nil
}

func (r *Router) SetRolloutTarget(name string, targetURL string, deployTimeout time.Duration, drainTimeout time.Duration) error {
	defer r.saveStateSnapshot()

	slog.Info("Deploying for rollout", "service", name, "target", targetURL)

	service := r.serviceForName(name)
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
	return nil
}

func (r *Router) SetRolloutSplit(name string, percent int, allowList []string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.SetRolloutSplit(percent, allowList)
}

func (r *Router) StopRollout(name string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.StopRollout()
}

func (r *Router) RemoveService(name string) error {
	defer r.saveStateSnapshot()

	err := r.withWriteLock(func() error {
		service := r.services[name]
		if service == nil {
			return ErrorServiceNotFound
		}

		service.SetTarget(TargetSlotActive, nil, DefaultDrainTimeout)
		delete(r.services, service.name)
		r.hostServices = r.services.HostServices()

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (r *Router) PauseService(name string, drainTimeout time.Duration, pauseTimeout time.Duration) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Pause(drainTimeout, pauseTimeout)
}

func (r *Router) StopService(name string, drainTimeout time.Duration, message string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Stop(drainTimeout, message)
}

func (r *Router) ResumeService(name string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.Resume()
}

func (r *Router) ListActiveServices() ServiceDescriptionMap {
	result := ServiceDescriptionMap{}

	r.withReadLock(func() error {
		for name, service := range r.services {
			host := service.host
			if host == "" {
				host = "*"
			}
			if service.active != nil {
				result[name] = ServiceDescription{
					Host:   host,
					Target: service.active.Target(),
					TLS:    service.options.RequireTLS(),
					State:  service.pauseController.GetState().String(),
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

	service, ok := r.hostServices[host]
	if !ok {
		service = r.hostServices[""]
	}

	return service
}

func (r *Router) setActiveTarget(name string, host string, target *Target, options ServiceOptions, drainTimeout time.Duration) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

	service := r.services[name]
	if service == nil {
		service = NewService(name, host, options)
	} else {
		service.UpdateOptions(host, options)
	}

	conflict := r.services.CheckHostAvailability(service, host)
	if conflict != nil {
		slog.Error("Host in use by another service", "service", conflict.name, "host", host)
		return ErrorHostInUse
	}
	r.services[name] = service
	r.hostServices = r.services.HostServices()

	service.SetTarget(TargetSlotActive, target, drainTimeout)

	return nil
}

func (r *Router) serviceForName(name string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services[name]
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
