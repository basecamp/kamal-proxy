package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
)

type serviceMap map[url.URL]*Service

type LoadBalancer struct {
	config         Config
	services       serviceMap
	activeServices []*Service
	serviceLock    sync.RWMutex
	serviceIndex   int
}

var (
	ErrorServiceAlreadyExists         = errors.New("Service already exists")
	ErrorServiceNotFound              = errors.New("Service not found")
	ErrorServiceFailedToBecomeHealthy = errors.New("Service failed to become healthy")
)

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

func (lb *LoadBalancer) Add(hostURLs []*url.URL, waitForHealthy bool) error {
	services, err := lb.addServicesUnlessExists(hostURLs)
	if err != nil {
		log.Err(err).Msg("Unable to add services")
		return err
	}

	for _, service := range services {
		log.Info().Str("host", service.Host()).Msg("Service added")
		service.BeginHealthChecks(lb)
	}

	if waitForHealthy {
		for _, service := range services {
			healthy := service.WaitUntilHealthy(lb.config.AddTimeout)
			if !healthy {
				log.Info().Str("host", service.Host()).Msg("Service failed to become healthy")
				return ErrorServiceFailedToBecomeHealthy
			}

			log.Info().Str("host", service.Host()).Msg("Service is now healthy")
		}
	}

	return nil
}

func (lb *LoadBalancer) Remove(hostURLs []*url.URL) error {
	services, err := lb.removeAndReturnServices(hostURLs)
	if err != nil {
		log.Err(err).Msg("Unable to remove services")
		return err
	}

	for _, service := range services {
		// TODO: drain in parallel -- maybe split "start drain" and "wait for drain"?
		log.Info().Str("host", service.Host()).Msg("Draining service")
		service.Drain(lb.config.DrainTimeout)
		log.Info().Str("host", service.Host()).Msg("Removed service")
	}

	return nil
}

func (lb *LoadBalancer) RestoreFromStateFile() error {
	var sf stateFile

	f, err := os.Open(lb.config.StatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Info().Msg("No state file present; starting empty")
			return nil
		}
		log.Err(err).Msg("Failed to open state file")
		return err
	}
	defer f.Close()

	err = json.NewDecoder(f).Decode(&sf)
	if err != nil {
		log.Err(err).Msg("Failed to read file")
		return err
	}

	hostURLs := []*url.URL{}
	for _, hostname := range sf.Hosts {
		hostURL, err := url.Parse("http://" + hostname)
		if err == nil {
			hostURLs = append(hostURLs, hostURL)
		}
	}

	err = lb.Add(hostURLs, false)
	if err != nil {
		log.Err(err).Msg("Failed to restore services from state file")
		return err
	}

	log.Info().Msg("Restored previous state")

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

func (lb *LoadBalancer) addServicesUnlessExists(hostURLs []*url.URL) ([]*Service, error) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	services := []*Service{}
	for _, hostURL := range hostURLs {
		if lb.services[*hostURL] == nil {
			service := NewService(hostURL)
			lb.services[*hostURL] = service
			services = append(services, service)
		} else {
			log.Info().Str("host", hostURL.Host).Msg("Service already exists; ignoring")
		}
	}

	if len(services) == 0 && len(hostURLs) > 0 {
		return nil, ErrorServiceAlreadyExists
	}

	lb.updateActive()
	lb.writeStateFile()

	return services, nil
}

func (lb *LoadBalancer) removeAndReturnServices(hostURLs []*url.URL) ([]*Service, error) {
	lb.serviceLock.Lock()
	defer lb.serviceLock.Unlock()

	services := []*Service{}
	for _, hostURL := range hostURLs {
		service := lb.services[*hostURL]
		if service != nil {
			services = append(services, service)
			delete(lb.services, *hostURL)
		} else {
			log.Info().Str("host", hostURL.Host).Msg("Service not found; ignoring")
		}
	}

	if len(services) == 0 && len(hostURLs) > 0 {
		return nil, ErrorServiceNotFound
	}

	lb.updateActive()
	lb.writeStateFile()

	return services, nil
}

type stateFile struct {
	Hosts []string `json:"hosts"`
}

func (lb *LoadBalancer) writeStateFile() error {
	sf := stateFile{
		Hosts: []string{},
	}
	for hostURL := range lb.services {
		sf.Hosts = append(sf.Hosts, hostURL.Host)
	}

	f, err := os.Create(lb.config.StatePath())
	if err != nil {
		log.Err(err).Msg("Failed to create state file")
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(sf)
}
