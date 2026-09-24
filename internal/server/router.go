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
	"slices"
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

func RoutedTargetPath(r *http.Request) string {
	path := r.URL.Path
	if rc := RoutingContext(r); rc != nil {
		path = strings.TrimPrefix(path, rc.MatchedPrefix)
		if path == "" {
			path = rootPath
		}
	}
	return path
}

type Router struct {
	statePath    string
	dockerClient *DockerClient
	services     *ServiceMap
	serviceLock  sync.RWMutex
	stateLock    sync.Mutex
}

type ServiceDescription struct {
	Hosts        nonNullList        `json:"hosts"`
	PathPrefixes nonNullList        `json:"path_prefixes"`
	TLS          bool               `json:"tls"`
	Targets      nonNullList        `json:"targets"`
	ReadTargets  nonNullList        `json:"read_targets"`
	State        string             `json:"state"`
	Rollout      RolloutDescription `json:"rollout"`
}

func (sd ServiceDescription) DisplayHosts() string {
	return strings.Join(sd.Hosts, ",")
}

func (sd ServiceDescription) DisplayPaths() string {
	return strings.Join(sd.PathPrefixes, ",")
}

func (sd ServiceDescription) DisplayTargets() string {
	return strings.Join(slices.Concat(sd.Targets, sd.ReadTargets), ",")
}

type RolloutDescription struct {
	Enabled     bool        `json:"enabled"`
	Percentage  int         `json:"percentage"`
	Allowlist   nonNullList `json:"allowlist"`
	Targets     nonNullList `json:"targets"`
	ReadTargets nonNullList `json:"read_targets"`
}

type ServiceDescriptionMap map[string]ServiceDescription

type nonNullList []string

func (l nonNullList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(l))
}

func NewRouter(statePath string, dockerSocketPath string) *Router {
	return &Router{
		statePath:    statePath,
		dockerClient: NewDockerClient(dockerSocketPath),
		services:     NewServiceMap(),
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

	return r.withWriteLock(func() error {
		r.services = NewServiceMap()
		for _, service := range services {
			service.lifecycle = r.dockerClient
			service.stateChanged = func() { _ = r.saveStateSnapshot() }
			if err := service.initialize(service.options, service.targetOptions); err != nil {
				return fmt.Errorf("initialize restored service %q: %w", service.name, err)
			}
			r.services.Set(service)
		}
		slog.Info("Restored saved state", "path", r.statePath)
		return nil
	})
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

func (r *Router) DeployService(name string, targetURLs, readerURLs []string, options ServiceOptions, targetOptions TargetOptions, deploymentOptions DeploymentOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}

	options.Normalize()
	slog.Info("Deploying", "service", name, "targets", targetURLs, "hosts", options.Hosts, "paths", options.PathPrefixes, "tls", options.TLSEnabled)

	lb, err := r.createLoadBalancer(targetURLs, readerURLs, options, targetOptions, deploymentOptions)
	if err != nil {
		return err
	}

	replaced, err := r.installLoadBalancer(name, TargetSlotActive, lb, options, func() (*Service, error) {
		return r.createOrUpdateService(name, options, targetOptions)
	})
	if err != nil {
		return err
	}

	if replaced != nil {
		replaced.Dispose()
		replaced.DrainAll(deploymentOptions.DrainTimeout)
	}

	slog.Info("Deployed", "service", name, "targets", targetURLs, "hosts", options.Hosts, "paths", options.PathPrefixes, "tls", options.TLSEnabled)
	return nil
}

func (r *Router) SetRolloutTargets(name string, targetURLs, readerURLs []string, deploymentOptions DeploymentOptions) error {
	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	slog.Info("Deploying for rollout", "service", name, "targets", targetURLs)

	lb, err := r.createLoadBalancer(targetURLs, readerURLs, service.options, service.targetOptions, deploymentOptions)
	if err != nil {
		return err
	}

	replaced, err := r.installLoadBalancer(name, TargetSlotRollout, lb, service.options, func() (*Service, error) {
		return service, nil
	})
	if err != nil {
		return err
	}

	if replaced != nil {
		replaced.Dispose()
		replaced.DrainAll(deploymentOptions.DrainTimeout)
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

func (r *Router) EnableRollout(name string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.EnableRollout()
}

func (r *Router) DisableRollout(name string) error {
	defer r.saveStateSnapshot()

	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	return service.DisableRollout()
}

func (r *Router) RemoveRolloutTargets(name string, drainTimeout time.Duration) error {
	service := r.serviceForName(name)
	if service == nil {
		return ErrorServiceNotFound
	}

	removed, err := service.RemoveRollout()
	if err != nil {
		return err
	}

	_ = r.saveStateSnapshot()

	removed.Dispose()
	removed.DrainAll(drainTimeout)

	return nil
}

func (r *Router) RemoveService(name string) error {
	defer r.saveStateSnapshot()

	return r.withWriteLock(func() error {
		service := r.services.Get(name)
		if service == nil {
			return ErrorServiceNotFound
		}

		service.Dispose()
		r.services.Remove(service.name)

		return nil
	})
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
			if service.active != nil {
				result[name] = service.Describe()
			}
		}
		return nil
	})

	return result
}

func (r *Router) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello.ServerName == "" {
		hello.ServerName = r.defaultTLSHostname()

		if hello.ServerName == "" {
			slog.Debug("ACME: Unable to get certificate (no server name)")
			return nil, ErrorNoServerName
		} else {
			slog.Warn("No server name; using default TLS hostname", "host", hello.ServerName)
		}
	}

	service := r.serviceForHost(hello.ServerName)
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

func (r *Router) createOrUpdateService(name string, options ServiceOptions, targetOptions TargetOptions) (*Service, error) {
	service := r.services.Get(name)
	if service == nil {
		service, err := NewService(name, options, targetOptions, r.dockerClient)
		if err == nil {
			service.stateChanged = func() { _ = r.saveStateSnapshot() }
			if service.idleController != nil {
				service.idleController.SetPersist(service.stateChanged)
			}
		}
		return service, err
	}

	service.lifecycle = r.dockerClient
	err := service.UpdateOptions(options, targetOptions)
	return service, err
}

func (r *Router) createLoadBalancer(targetURLs, readerURLs []string, options ServiceOptions, targetOptions TargetOptions, deploymentOptions DeploymentOptions) (*LoadBalancer, error) {
	tl, err := NewTargetList(targetURLs, readerURLs, targetOptions)
	if err != nil {
		return nil, err
	}

	lb := NewLoadBalancer(tl, options.WriterAffinityTimeout, options.ReadTargetsAcceptWebsockets)

	if !deploymentOptions.Force {
		err = lb.WaitUntilHealthy(deploymentOptions.DeployTimeout)
		if err != nil {
			lb.Dispose()
			return nil, err
		}
	}

	return lb, nil
}

func (r *Router) installLoadBalancer(name string, slot TargetSlot, lb *LoadBalancer, options ServiceOptions, getService func() (*Service, error)) (*LoadBalancer, error) {
	defer r.saveStateSnapshot()

	var replaced *LoadBalancer

	err := r.withWriteLock(func() error {
		conflict := r.services.CheckAvailability(name, options)
		if conflict != nil {
			slog.Error("Host settings conflict with another service", "service", conflict.name)
			return ErrorHostInUse
		}

		service, err := getService()
		if err != nil {
			return err
		}

		replaced = service.UpdateLoadBalancer(lb, slot)
		r.services.Set(service)
		return nil
	})

	return replaced, err
}

func (r *Router) saveStateSnapshot() error {
	r.stateLock.Lock()
	defer r.stateLock.Unlock()
	services := []*Service{}
	r.withReadLock(func() error {
		for _, service := range r.services.All() {
			services = append(services, service)
		}
		return nil
	})

	f, err := os.Create(r.statePath)
	if err != nil {
		slog.Error("Unable to save state snapshot", "error", err, "path", r.statePath)
		return err
	}

	encodeErr := json.NewEncoder(f).Encode(services)
	closeErr := f.Close()
	if encodeErr != nil {
		slog.Error("Unable to save state", "error", encodeErr, "path", r.statePath)
		return encodeErr
	}
	if closeErr != nil {
		slog.Error("Unable to close saved state", "error", closeErr, "path", r.statePath)
		return closeErr
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

func (r *Router) serviceForName(name string) *Service {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.Get(name)
}

func (r *Router) defaultTLSHostname() string {
	r.serviceLock.RLock()
	defer r.serviceLock.RUnlock()

	return r.services.DefaultTLSHostname()
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
