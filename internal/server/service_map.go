package server

import (
	"iter"
	"net"
	"net/http"
	"strings"
)

type ServiceMap struct {
	services       map[string]*Service
	hostServiceMap map[string]*Service
}

func NewServiceMap() *ServiceMap {
	return &ServiceMap{
		services:       map[string]*Service{},
		hostServiceMap: map[string]*Service{},
	}
}

func (m *ServiceMap) Get(name string) *Service {
	return m.services[name]
}

func (m *ServiceMap) Set(service *Service) {
	m.services[service.name] = service
	m.updateHostMappings()
}

func (m *ServiceMap) Remove(name string) {
	delete(m.services, name)
	m.updateHostMappings()
}

func (m *ServiceMap) All() iter.Seq2[string, *Service] {
	return func(yield func(string, *Service) bool) {
		for name, service := range m.services {
			if !yield(name, service) {
				return
			}
		}
	}
}

func (m *ServiceMap) CheckHostAvailability(name string, hosts []string) *Service {
	if len(hosts) == 0 {
		hosts = []string{""}
	}

	for _, host := range hosts {
		service := m.hostServiceMap[host]
		if service != nil && service.name != name {
			return service
		}
	}
	return nil
}

func (m *ServiceMap) ServiceForHost(host string) *Service {
	service, ok := m.hostServiceMap[host]
	if ok {
		return service
	}

	sep := strings.Index(host, ".")
	if sep > 0 {
		service, ok := m.hostServiceMap["*"+host[sep:]]
		if ok {
			return service
		}
	}

	return m.hostServiceMap[""]
}

func (m *ServiceMap) ServiceForRequest(req *http.Request) *Service {
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}

	return m.ServiceForHost(host)
}

// Private

func (m *ServiceMap) updateHostMappings() {
	hostServices := map[string]*Service{}

	for _, service := range m.services {
		if len(service.hosts) == 0 {
			hostServices[""] = service
			continue
		}
		for _, host := range service.hosts {
			hostServices[host] = service
		}
	}

	m.hostServiceMap = hostServices
}
