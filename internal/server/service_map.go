package server

import (
	"iter"
	"maps"
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

type requestRoute struct {
	service             *Service
	pathPrefix          string
	coveringRootService *Service
}

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

func (m *ServiceMap) HostBypassingClientCAAfterSet(name string, options ServiceOptions) string {
	serviceOptions := map[string]ServiceOptions{}
	for serviceName, service := range m.services {
		serviceOptions[serviceName] = service.options
	}
	serviceOptions[name] = options

	return hostBypassingClientCA(serviceOptions)
}

func (m *ServiceMap) ServiceForHost(host string) *Service {
	service, _ := m.serviceFor(host, rootPath)
	return service
}

func (m *ServiceMap) ServiceForRequest(req *http.Request) (*Service, string) {
	route := m.RouteForRequest(req)
	return route.service, route.pathPrefix
}

func (m *ServiceMap) RouteForRequest(req *http.Request) requestRoute {
	host := requestHost(req)
	bindings := m.bindingsForHost(host)
	service, pathPrefix := serviceForPath(bindings, req.URL.Path)

	coveringRootService := rootServiceOf(bindings)
	if coveringRootService == nil {
		coveringRootService = m.rootServiceCovering(host)
	}

	return requestRoute{service: service, pathPrefix: pathPrefix, coveringRootService: coveringRootService}
}

// Private

func (m *ServiceMap) serviceFor(host, path string) (*Service, string) {
	return serviceForPath(m.bindingsForHost(host), path)
}

func serviceForPath(bindings []*pathBinding, path string) (*Service, string) {
	for _, binding := range bindings {
		if strings.HasPrefix(EnsureTrailingSlash(path), EnsureTrailingSlash(binding.pathPrefix)) {
			return binding.service, binding.pathPrefix
		}
	}

	return nil, ""
}

func (m *ServiceMap) rootServiceCovering(host string) *Service {
	for _, coveringHost := range [...]string{host, wildcardHost(host), ""} {
		if rootService := rootServiceOf(m.requestServiceMap[coveringHost]); rootService != nil {
			return rootService
		}
	}
	return nil
}

func rootServiceOf(bindings []*pathBinding) *Service {
	if len(bindings) == 0 {
		return nil
	}

	shortestPrefixBinding := bindings[len(bindings)-1]
	if shortestPrefixBinding.pathPrefix != rootPath {
		return nil
	}
	return shortestPrefixBinding.service
}

func (m *ServiceMap) bindingsForHost(host string) []*pathBinding {
	bindings, ok := m.requestServiceMap[host]
	if ok {
		return bindings
	}

	if wildcard := wildcardHost(host); wildcard != "" {
		bindings, ok = m.requestServiceMap[wildcard]
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
		if service.options.TLSEnabled && len(service.options.Hosts) > 0 && service.options.Hosts[0] != "" {
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

func requestHost(req *http.Request) string {
	host := req.Host

	if strings.Index(host, ":") > 0 {
		splitHost, _, err := net.SplitHostPort(host)
		if err == nil {
			host = splitHost
		}
	}

	return host
}

func hostBypassingClientCA(serviceOptions map[string]ServiceOptions) string {
	hosts := map[string]bool{}
	rootServiceOptions := map[string]ServiceOptions{}

	for _, options := range serviceOptions {
		for _, host := range options.Hosts {
			hosts[host] = true
			if slices.Contains(options.PathPrefixes, rootPath) {
				rootServiceOptions[host] = options
			}
		}
	}

	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		if _, hasRootService := rootServiceOptions[host]; hasRootService {
			continue
		}

		shadowedRootService, ok := rootServiceOptions[shadowedHost(host, hosts)]
		if ok && shadowedRootService.RequiresClientCertificate() {
			return host
		}
	}

	return ""
}

func shadowedHost(host string, hosts map[string]bool) string {
	if wildcard := wildcardHost(host); wildcard != host && hosts[wildcard] {
		return wildcard
	}
	return ""
}

func wildcardHost(host string) string {
	sep := strings.Index(host, ".")
	if sep > 0 {
		return "*" + host[sep:]
	}
	return ""
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
