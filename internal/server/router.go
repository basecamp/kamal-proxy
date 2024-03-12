package server

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var (
	ErrorServiceNotFound             = errors.New("service not found")
	ErrorTargetFailedToBecomeHealthy = errors.New("target failed to become healthy")
)

type Service struct {
	active   *Target
	adding   *Target
	draining []*Target
}

type HostServiceMap map[string]*Service

type Router struct {
	services    HostServiceMap
	serviceLock sync.RWMutex
}

func NewRouter() *Router {
	return &Router{
		services: HostServiceMap{},
	}
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	target := r.activeTargetForRequest(req)
	if target == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		target.ServeHTTP(w, req)
	}
}

func (r *Router) SetServiceTarget(host string, target *Target, addTimeout time.Duration) error {
	slog.Info("Deploying", "host", host, "target", target.targetURL.Host, "ssl", target.requireSSL)

	service := r.setAddingService(host, target)

	target.BeginHealthChecks()
	becameHealthy := target.WaitUntilHealthy(addTimeout)
	if !becameHealthy {
		slog.Info("Target failed to become healthy", "host", host, "target", target.targetURL.Host)
		r.setAddingService(host, nil)
		return ErrorTargetFailedToBecomeHealthy
	}

	r.promoteToActive(service, target)
	slog.Info("Deployed", "host", host, "target", target.targetURL.Host)
	return nil
}

func (r *Router) RemoveService(host string) error {
	r.serviceLock.Lock()
	defer r.serviceLock.Unlock()

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
}

func (r *Router) ListActiveServices() map[string]string {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	result := map[string]string{}
	for host, service := range r.services {
		if host == "" {
			host = "*"
		}
		if service.active != nil {
			result[host] = service.active.targetURL.Host
		}
	}

	return result
}

func (r *Router) ValidateSSLDomain(host string) bool {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service, ok := r.services[host]
	if ok && service.active != nil {
		return service.active.requireSSL
	}

	return false
}

// Private

func (r *Router) activeTargetForRequest(req *http.Request) *Target {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	service, ok := r.services[req.Host]
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

		r.serviceLock.Lock()
		defer r.serviceLock.Unlock()

		service.draining = removeItem(service.draining, target)
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
