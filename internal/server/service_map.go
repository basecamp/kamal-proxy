package server

import (
	"net"
	"net/http"
	"strings"
)

type (
	ServiceMap     map[string]*Service
	HostServiceMap map[string]*Service
)

func (m ServiceMap) HostServices() HostServiceMap {
	hostServices := HostServiceMap{}
	for _, service := range m {
		if len(service.hosts) == 0 {
			hostServices[""] = service
			continue
		}
		for _, host := range service.hosts {
			hostServices[host] = service
		}
	}
	return hostServices
}

func (m HostServiceMap) CheckHostAvailability(name string, hosts []string) *Service {
	if len(hosts) == 0 {
		hosts = []string{""}
	}

	for _, host := range hosts {
		service := m[host]
		if service != nil && service.name != name {
			return service
		}
	}
	return nil
}

func (m HostServiceMap) ServiceForHost(host string) *Service {
	service, ok := m[host]
	if ok {
		return service
	}

	sep := strings.Index(host, ".")
	if sep > 0 {
		service, ok := m["*"+host[sep:]]
		if ok {
			return service
		}
	}

	return m[""]
}

func (m HostServiceMap) ServiceForRequest(req *http.Request) *Service {
	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}

	return m.ServiceForHost(host)
}
