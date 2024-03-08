package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
)

var (
	ErrorInvalidHostPattern           = errors.New("invalid host pattern")
	ErrorServiceAlreadyExists         = errors.New("service already exists")
	ErrorServiceNotFound              = errors.New("service not found")
	ErrorServiceFailedToBecomeHealthy = errors.New("service failed to become healthy")
)

type Host string
type Hosts []Host

var hostRegex = regexp.MustCompile(`^(\w[-_.\w+]+)(:\d+)?$`)

func NewHost(host string) (Host, error) {
	if !hostRegex.MatchString(host) {
		return "", fmt.Errorf("%s :%w", host, ErrorInvalidHostPattern)
	}
	return Host(host), nil
}

func (h Host) String() string {
	return string(h)
}

func (h Host) ToURL() (*url.URL, error) {
	return url.Parse("http://" + string(h))
}

type serviceMap map[Host]*Service

type LoadBalancer struct {
	config         Config
	services       serviceMap
	activeServices []*Service
	serviceLock    sync.RWMutex
	serviceIndex   int
}

func NewLoadBalancer(config Config) *LoadBalancer {
	return &LoadBalancer{
		config:   config,
		services: make(serviceMap),
	}
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	service := lb.nextServiceForRequest()
	if service == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		service.ServeHTTP(w, r)
	}
}

func (lb *LoadBalancer) GetServices() []*Service {
	lb.serviceLock.RLock()
	defer lb.serviceLock.RUnlock()

	result := []*Service{}
	for _, service := range lb.services {
		result = append(result, service)
	}

	return result
}

func (lb *LoadBalancer) Add(hosts Hosts, waitForHealthy bool) error {
	services, err := lb.addServicesUnlessExists(hosts)
	if err != nil {
		slog.Error("Unable to add services", "error", err)
		return err
	}

	for _, service := range services {
		slog.Info("Service added", "host", service.Host())
		service.BeginHealthChecks(lb)
	}

	if waitForHealthy {
		for _, service := range services {
			healthy := service.WaitUntilHealthy(lb.config.AddTimeout)
			if !healthy {
				slog.Info("Service failed to become healthy", "host", service.Host())
				return ErrorServiceFailedToBecomeHealthy
			}

			slog.Info("Service is now healthy", "host", service.Host())
		}
	}

	return nil
}

func (lb *LoadBalancer) Remove(hosts Hosts) error {
	services, err := lb.removeAndReturnServices(hosts)
	if err != nil {
		slog.Error("Unable to remove services", "error", err)
		return err
	}

	for _, service := range services {
		// TODO: drain in parallel -- maybe split "start drain" and "wait for drain"?
		slog.Info("Draining service", "host", service.Host())
		service.Drain(lb.config.DrainTimeout)
		slog.Info("Removed service", "host", service.Host())
	}

	return nil
}

func (lb *LoadBalancer) Deploy(hosts Hosts) error {
	toAdd, toRemove := lb.determineDeploymentChanges(hosts)

	if len(toAdd) > 0 {
		err := lb.Add(toAdd, true)
		if err != nil {
			slog.Error("Failed to deploy new services", "error", err)
			return err
		}
	}

	if len(toRemove) > 0 {
		err := lb.Remove(toRemove)
		if err != nil {
			slog.Error("Failed to remove old services", "error", err)
			return err
		}
	}

	return nil
}

func (lb *LoadBalancer) RestoreFromStateFile() error {
	var sf stateFile

	f, err := os.Open(lb.config.StatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("No state file present; starting empty")
			return nil
		}
		slog.Error("Failed to open state file", "error", err)
		return err
	}
	defer f.Close()

	err = json.NewDecoder(f).Decode(&sf)
	if err != nil {
		slog.Error("Failed to read state file", "error", err)
		return err
	}

	err = lb.Add(sf.Hosts, false)
	if err != nil {
		slog.Error("Failed to restore services from state file", "error", err)
		return err
	}

	slog.Info("Restored previous state")

	return nil
}

// ServiceStateChangeConsumer

func (lb *LoadBalancer) StateChanged(service *Service) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	lb.updateActive()
}

// Private

func (lb *LoadBalancer) nextServiceForRequest() *Service {
	lb.serviceLock.RLock()
	defer lb.serviceLock.RUnlock()

	activeCount := len(lb.activeServices)
	if activeCount == 0 {
		return nil
	}

	lb.serviceIndex = (lb.serviceIndex + 1) % activeCount
	service := lb.activeServices[lb.serviceIndex]

	return service
}

func (lb *LoadBalancer) updateActive() {
	lb.activeServices = []*Service{}
	for _, service := range lb.services {
		if service.state == ServiceStateHealthy {
			lb.activeServices = append(lb.activeServices, service)
		}
	}
}

func (lb *LoadBalancer) addServicesUnlessExists(hosts Hosts) ([]*Service, error) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	services := []*Service{}
	for _, host := range hosts {
		if lb.services[host] == nil {
			hostURL, err := host.ToURL()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", host, ErrorInvalidHostPattern)
			}

			service := NewService(hostURL, lb.config.HealthCheckConfig)
			lb.services[host] = service

			services = append(services, service)
		} else {
			slog.Info("Service already exists; ignoring", "host", host)
		}
	}

	if len(services) == 0 && len(hosts) > 0 {
		return nil, ErrorServiceAlreadyExists
	}

	lb.updateActive()
	lb.writeStateFile()

	return services, nil
}

func (lb *LoadBalancer) removeAndReturnServices(hosts Hosts) ([]*Service, error) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	services := []*Service{}
	for _, host := range hosts {
		service := lb.services[host]
		if service != nil {
			services = append(services, service)
			delete(lb.services, host)
		} else {
			slog.Info("Service not found; ignoring", "host", host)
		}
	}

	if len(services) == 0 && len(hosts) > 0 {
		return nil, ErrorServiceNotFound
	}

	lb.updateActive()
	lb.writeStateFile()

	return services, nil
}

func (lb *LoadBalancer) determineDeploymentChanges(hosts Hosts) (Hosts, Hosts) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	toAdd := Hosts{}
	toRemove := Hosts{}

	isBeingDeployed := func(host Host) bool {
		for _, h := range hosts {
			if h == host {
				return true
			}
		}
		return false
	}

	for _, host := range hosts {
		if lb.services[host] == nil {
			toAdd = append(toAdd, host)
		}
	}

	for host := range lb.services {
		if !isBeingDeployed(host) {
			toRemove = append(toRemove, host)
		}
	}

	return toAdd, toRemove
}

type stateFile struct {
	Hosts Hosts `json:"hosts"`
}

func (lb *LoadBalancer) writeStateFile() error {
	sf := stateFile{
		Hosts: Hosts{},
	}
	for host := range lb.services {
		sf.Hosts = append(sf.Hosts, host)
	}

	f, err := os.Create(lb.config.StatePath())
	if err != nil {
		slog.Error("Failed to create state file", "error", err)
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(sf)
}
