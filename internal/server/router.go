package server

import (
	"context"
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

	contextKeyRoutingContext = contextKey("routing-context")
)

type routingContext struct {
	MatchedPrefix string
}

func RoutingContext(r *http.Request) *routingContext {
	rc, ok := r.Context().Value(contextKeyRoutingContext).(*routingContext)
	if !ok {
		return nil
	}
	return rc
}

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
	service, prefix := r.serviceForRequest(req)
	if service == nil {
		SetErrorResponse(w, req, http.StatusNotFound, nil)
		return
	}

	if service.options.StripPrefix && prefix != rootPath {
		ctx := context.WithValue(req.Context(), contextKeyRoutingContext, &routingContext{MatchedPrefix: prefix})
		req = req.WithContext(ctx)
	}

	service.ServeHTTP(w, req)
}

func (r *Router) SetServiceTargets(name string, hosts []string, pathPrefixes []string, targetURLs []string,
	options ServiceOptions, targetOptions TargetOptions,
	deployTimeout time.Duration, drainTimeout time.Duration,
) error {
	defer r.saveStateSnapshot()

	slog.Info("Deploying", "service", name, "hosts", hosts, "paths", pathPrefixes, "targets", targetURLs, "tls", options.TLSEnabled, "strip", options.StripPrefix)

	targets := make([]*Target, 0, len(targetURLs))
	for _, targetURL := range targetURLs {
		target, err := r.deployNewTargetWithOptions(targetURL, targetOptions, deployTimeout)
		if err != nil {
			for _, t := range targets {
				t.StopHealthChecks()
			}
			return err
		}
		targets = append(targets, target)
	}

	err := r.setActiveTargets(name, hosts, pathPrefixes, targets, options, drainTimeout)
	if err != nil {
		for _, target := range targets {
			target.StopHealthChecks()
		}
		return err
	}

	slog.Info("Deployed", "service", name, "hosts", hosts, "targets", targetURLs)
	return nil
}

func (r *Router) SetRolloutTargets(name string, targetURLs []string, deployTimeout time.Duration, drainTimeout time.Duration) error {
	defer r.saveStateSnapshot()

	slog.Info("Deploying for rollout", "service", name, "targets", targetURLs)

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	activeTargets := service.activePool.GetTargets()
	if len(activeTargets) == 0 {
		return ErrorNoHealthyTargets
	}
	targetOptions := activeTargets[0].options

	var newTargets []*Target
	var deployedTargets []*Target

	for _, targetURL := range targetURLs {
		target, err := r.deployNewTargetWithOptions(targetURL, targetOptions, deployTimeout)
		if err != nil {
			for _, t := range deployedTargets {
				t.StopHealthChecks()
			}
			return err
		}

		newTargets = append(newTargets, target)
		deployedTargets = append(deployedTargets, target)
	}

	oldTargets := service.rolloutPool.GetTargets()

	service.rolloutPool.ReplaceTargets(newTargets)

	for _, oldTarget := range oldTargets {
		oldTarget.Drain(drainTimeout)
	}

	slog.Info("Deployed for rollout", "service", name, "targets", targetURLs)
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
			activeTargets := service.activePool.GetTargets()
			if len(activeTargets) > 0 {
				host := strings.Join(service.hosts, ",")
				if host == "" {
					host = "*"
				}

				path := strings.Join(service.pathPrefixes, ",")

				result[name] = ServiceDescription{
					Host:   host,
					Path:   path,
					Target: activeTargets[0].Target(),
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

func (r *Router) serviceForRequest(req *http.Request) (*Service, string) {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.ServiceForRequest(req)
}

func (r *Router) serviceForHost(host string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.ServiceForHost(host)
}

func (r *Router) setActiveTargets(name string, hosts []string, pathPrefixes []string, targets []*Target, options ServiceOptions, drainTimeout time.Duration) error {
	var replacedTargets []*Target

	err := r.withWriteLock(func() error {
		conflict := r.services.CheckAvailability(name, hosts, pathPrefixes)
		if conflict != nil {
			slog.Error("Host settings conflict with another service", "service", conflict.name)
			return ErrorHostInUse
		}

		var err error
		service := r.services.Get(name)
		if service == nil {
			service, err = NewService(name, hosts, pathPrefixes, options)
		} else {
			err = service.UpdateOptions(hosts, pathPrefixes, options)
		}
		if err != nil {
			return err
		}

		r.services.Set(service)

		replacedTargets = service.activePool.GetTargets()
		service.activePool.ReplaceTargets(targets)

		return nil
	})
	if err != nil {
		return err
	}

	for _, replacedTarget := range replacedTargets {
		replacedTarget.Drain(drainTimeout)
	}

	return nil
}

func (r *Router) setActiveTarget(name string, hosts []string, pathPrefixes []string, target *Target, options ServiceOptions, drainTimeout time.Duration) error {
	var replacedTarget *Target

	err := r.withWriteLock(func() error {
		conflict := r.services.CheckAvailability(name, hosts, pathPrefixes)
		if conflict != nil {
			slog.Error("Host settings conflict with another service", "service", conflict.name)
			return ErrorHostInUse
		}

		var err error
		service := r.services.Get(name)
		if service == nil {
			service, err = NewService(name, hosts, pathPrefixes, options)
		} else {
			err = service.UpdateOptions(hosts, pathPrefixes, options)
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
