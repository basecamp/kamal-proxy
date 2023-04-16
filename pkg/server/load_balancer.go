package server

import (
	"errors"
	"net/http"
	"net/url"
	"sync"

	"github.com/rs/zerolog/log"
)

type serviceMap map[url.URL]*Service

type LoadBalancer struct {
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

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{
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

func (lb *LoadBalancer) Add(hostURLs []*url.URL) error {
	services, err := lb.addServicesUnlessExists(hostURLs)
	if err != nil {
		log.Err(err).Msg("Unable to add services")
		return err
	}

	for _, service := range services {
		log.Info().Str("host", service.Host()).Msg("Service added")
		service.BeginHealthChecks(lb)
	}

	for _, service := range services {
		healthy := service.WaitUntilHealthy()
		if !healthy {
			log.Info().Str("host", service.Host()).Msg("Service failed to become healthy")
			return ErrorServiceFailedToBecomeHealthy
		}

		log.Info().Str("host", service.Host()).Msg("Service is now healthy")
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
		service.Drain()
		log.Info().Str("host", service.Host()).Msg("Removed service")
	}

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

	if len(services) == 0 {
		return nil, ErrorServiceAlreadyExists
	}

	lb.updateActive()
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

	if len(services) == 0 {
		return nil, ErrorServiceNotFound
	}

	lb.updateActive()
	return services, nil
}
