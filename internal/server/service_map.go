package server

import (
	"iter"
	"net"
	"net/http"
	"slices"
	"strings"
)

const (
	rootPath = "/"
)

type pathBinding struct {
	path    string
	service *Service
}

type requestServiceMap map[string][]*pathBinding

type ServiceMap struct {
	services          map[string]*Service
	requestServiceMap requestServiceMap
}

func NewServiceMap() *ServiceMap {
	return &ServiceMap{
		services:          map[string]*Service{},
		requestServiceMap: requestServiceMap{},
	}
}

func (m *ServiceMap) Get(name string) *Service {
	return m.services[name]
}

func (m *ServiceMap) Set(service *Service) {
	m.services[service.name] = service
	m.updateRequestServiceMap()
}

func (m *ServiceMap) Remove(name string) {
	delete(m.services, name)
	m.updateRequestServiceMap()
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

func (m *ServiceMap) CheckAvailability(name string, hosts, paths []string) *Service {
	paths = m.normalizePaths(paths)
	for _, host := range m.normalizeHosts(hosts) {
		bindings := m.requestServiceMap[host]
		for _, binding := range bindings {
			if slices.Contains(paths, binding.path) && binding.service.name != name {
				return binding.service
			}
		}
	}
	return nil
}

func (m *ServiceMap) ServiceForHost(host string) *Service {
	return m.serviceFor(host, rootPath)
}

func (m *ServiceMap) ServiceForRequest(req *http.Request) *Service {
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}

	return m.serviceFor(host, req.URL.Path)
}

// Private

func (m *ServiceMap) serviceFor(host, path string) *Service {
	bindings := m.bindingsForHost(host)
	if bindings == nil {
		return nil
	}

	for _, binding := range bindings {
		if strings.HasPrefix(path, binding.path) {
			return binding.service
		}
	}

	return nil
}

func (m *ServiceMap) bindingsForHost(host string) []*pathBinding {
	bindings, ok := m.requestServiceMap[host]
	if ok {
		return bindings
	}

	sep := strings.Index(host, ".")
	if sep > 0 {
		bindings, ok = m.requestServiceMap["*"+host[sep:]]
		if ok {
			return bindings
		}
	}

	return m.requestServiceMap[""]
}

func (m *ServiceMap) updateRequestServiceMap() {
	requestServiceMap := requestServiceMap{}

	for _, service := range m.services {
		for _, host := range m.normalizeHosts(service.hosts) {
			for _, path := range m.normalizePaths(service.paths) {
				bindings := requestServiceMap[host]
				if bindings == nil {
					bindings = []*pathBinding{}
				}
				bindings = append(bindings, &pathBinding{path: path, service: service})
				requestServiceMap[host] = bindings
			}
		}

		for _, bindings := range requestServiceMap {
			slices.SortFunc(bindings, func(a, b *pathBinding) int { return len(b.path) - len(a.path) })
		}
	}

	m.requestServiceMap = requestServiceMap
}

func (m *ServiceMap) normalizeHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return []string{""}
	}
	return hosts
}

func (m *ServiceMap) normalizePaths(paths []string) []string {
	if len(paths) == 0 {
		return []string{rootPath}
	}

	result := []string{}
	for _, path := range paths {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		result = append(result, path)
	}

	return result
}
