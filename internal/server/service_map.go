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
	pathPrefix string
	service    *Service
}

type requestServiceMap map[string][]*pathBinding

type ServiceMap struct {
	services           map[string]*Service
	requestServiceMap  requestServiceMap
	defaultTLSHostname string
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
	m.updateDefaultTLSHostname()
}

func (m *ServiceMap) Remove(name string) {
	delete(m.services, name)
	m.updateRequestServiceMap()
	m.updateDefaultTLSHostname()
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

func (m *ServiceMap) DefaultTLSHostname() string {
	return m.defaultTLSHostname
}

func (m *ServiceMap) CheckAvailability(name string, options ServiceOptions) *Service {
	for _, host := range options.Hosts {
		for _, pathPrefix := range options.PathPrefixes {
			bindings := m.requestServiceMap[host]
			for _, binding := range bindings {
				if pathPrefix == binding.pathPrefix && binding.service.name != name {
					return binding.service
				}
			}
		}
	}

	return nil
}

func (m *ServiceMap) ServiceForHost(host string) *Service {
	service, _ := m.serviceFor(host, rootPath)
	return service
}

func (m *ServiceMap) ServiceForRequest(req *http.Request) (*Service, string) {
	host := req.Host

	if strings.Index(host, ":") > 0 {
		splitHost, _, err := net.SplitHostPort(host)
		if err == nil {
			host = splitHost
		}
	}

	return m.serviceFor(host, req.URL.Path)
}

// Private

func (m *ServiceMap) serviceFor(host, path string) (*Service, string) {
	bindings := m.bindingsForHost(host)
	if bindings == nil {
		return nil, ""
	}

	for _, binding := range bindings {
		if strings.HasPrefix(EnsureTrailingSlash(path), EnsureTrailingSlash(binding.pathPrefix)) {
			return binding.service, binding.pathPrefix
		}
	}

	return nil, ""
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
		for _, host := range service.options.Hosts {
			for _, pathPrefix := range service.options.PathPrefixes {
				bindings := requestServiceMap[host]
				if bindings == nil {
					bindings = []*pathBinding{}
				}
				bindings = append(bindings, &pathBinding{pathPrefix: pathPrefix, service: service})
				requestServiceMap[host] = bindings
			}
		}
	}

	for _, bindings := range requestServiceMap {
		slices.SortFunc(bindings, func(a, b *pathBinding) int { return len(b.pathPrefix) - len(a.pathPrefix) })
	}

	m.requestServiceMap = requestServiceMap
	m.syncTLSOptionsFromRootDomain()
}

func (m *ServiceMap) updateDefaultTLSHostname() {
	for _, service := range m.services {
		if service.options.TLSEnabled && len(service.options.Hosts) > 0 {
			m.defaultTLSHostname = service.options.Hosts[0]
			return
		}
	}
}

func (m *ServiceMap) syncTLSOptionsFromRootDomain() {
	for _, service := range m.services {
		if !service.servesRootPath() {
			host := ""
			if len(service.options.Hosts) > 0 {
				host = service.options.Hosts[0]
			}

			rootService := m.ServiceForHost(host)
			if rootService != nil {
				service.options.TLSEnabled = rootService.options.TLSEnabled
				service.options.TLSRedirect = rootService.options.TLSRedirect
			} else {
				service.options.TLSEnabled = defaultServiceOptions.TLSEnabled
				service.options.TLSRedirect = defaultServiceOptions.TLSRedirect
			}
		}
	}
}

func NormalizeHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return []string{""}
	}
	return hosts
}

func NormalizePathPrefixes(pathPrefixes []string) []string {
	if len(pathPrefixes) == 0 {
		return []string{rootPath}
	}

	result := []string{}
	for _, pathPrefix := range pathPrefixes {
		result = append(result, "/"+strings.Trim(pathPrefix, "/"))
	}
	return result
}

func EnsureTrailingSlash(path string) string {
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}
