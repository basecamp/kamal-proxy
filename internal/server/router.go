package server

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrorServiceNotFound             = errors.New("service not found")
	ErrorTargetFailedToBecomeHealthy = errors.New("target failed to become healthy within configured timeout")
	ErrorHostInUse                   = errors.New("host settings conflict with another service")
	ErrorNoServerName                = errors.New("no server name provided")
	ErrorUnknownServerName           = errors.New("unknown server name")
)

type Router struct {
	statePath   string
	services    *ServiceMap
	serviceLock sync.RWMutex
}

type ServiceDescription struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	TLS    bool   `json:"tls"`
	Target string `json:"target"`
	State  string `json:"state"`
}

type ServiceDescriptionMap map[string]ServiceDescription

func NewRouter(statePath string) *Router {
	return &Router{
		statePath: statePath,
		services:  NewServiceMap(),
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
		r.services = NewServiceMap()
		for _, service := range services {
			r.services.Set(service)
		}

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

func (r *Router) SetServiceTarget(name string, hosts []string, pathPrefix string, targetURL string,
	options ServiceOptions, targetOptions TargetOptions,
	deployTimeout time.Duration, drainTimeout time.Duration,
) error {
	defer r.saveStateSnapshot()

	slog.Info("Deploying", "service", name, "hosts", hosts, "path", pathPrefix, "target", targetURL, "tls", options.TLSEnabled, "strip", targetOptions.StripPrefix)

	target, err := r.deployNewTargetWithOptions(targetURL, targetOptions, deployTimeout)
	if err != nil {
		return err
	}

	err = r.setActiveTarget(name, hosts, pathPrefix, target, options, drainTimeout)
	if err != nil {
		return err
	}

	slog.Info("Deployed", "service", name, "hosts", hosts, "target", targetURL)
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

	target, err := r.deployNewTargetWithOptions(targetURL, targetOptions, deployTimeout)
	if err != nil {
		return err
	}

	replacedTarget := service.SetTarget(TargetSlotRollout, target)
	if replacedTarget != nil {
		replacedTarget.Drain(drainTimeout)
	}

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

	var replacedTarget *Target

	err := r.withWriteLock(func() error {
		service := r.services.Get(name)
		if service == nil {
			return ErrorServiceNotFound
		}

		replacedTarget = service.SetTarget(TargetSlotActive, nil)
		r.services.Remove(service.name)

		return nil
	})
	if err != nil {
		return err
	}

	if replacedTarget != nil {
		replacedTarget.Drain(DefaultDrainTimeout)
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
		for name, service := range r.services.All() {
			host := strings.Join(service.hosts, ",")
			if host == "" {
				host = "*"
			}
			if service.active != nil {
				result[name] = ServiceDescription{
					Host:   host,
					Path:   service.pathPrefix,
					Target: service.active.Target(),
					TLS:    service.options.TLSEnabled,
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

func (r *Router) deployNewTargetWithOptions(targetURL string, targetOptions TargetOptions, deployTimeout time.Duration) (*Target, error) {
	target, err := NewTarget(targetURL, targetOptions)
	if err != nil {
		return nil, err
	}

	becameHealthy := target.WaitUntilHealthy(deployTimeout)
	if !becameHealthy {
		slog.Info("Target failed to become healthy", "target", targetURL)
		return nil, fmt.Errorf("%w (%s)", ErrorTargetFailedToBecomeHealthy, deployTimeout)
	}

	return target, nil
}

func (r *Router) saveStateSnapshot() error {
	services := []*Service{}
	r.withReadLock(func() error {
		for _, service := range r.services.All() {
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
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.ServiceForRequest(req)
}

func (r *Router) serviceForHost(host string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.ServiceForHost(host)
}

func (r *Router) setActiveTarget(name string, hosts []string, pathPrefix string, target *Target, options ServiceOptions, drainTimeout time.Duration) error {
	var replacedTarget *Target

	err := r.withWriteLock(func() error {
		conflict := r.services.CheckAvailability(name, hosts, pathPrefix)
		if conflict != nil {
			slog.Error("Host settings conflict with another service", "service", conflict.name)
			return ErrorHostInUse
		}

		var err error
		service := r.services.Get(name)
		if service == nil {
			service, err = NewService(name, hosts, pathPrefix, options)
		} else {
			err = service.UpdateOptions(hosts, pathPrefix, options)
		}
		if err != nil {
			return err
		}

		r.services.Set(service)
		replacedTarget = service.SetTarget(TargetSlotActive, target)

		return nil
	})
	if err != nil {
		return err
	}

	if replacedTarget != nil {
		replacedTarget.Drain(drainTimeout)
	}

	return nil
}

func (r *Router) serviceForName(name string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.Get(name)
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
