package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceMap_ServiceForHost(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"api.example.com"}})})
	sm.Set(&Service{name: "4", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"*.example.com"}})})
	sm.Set(&Service{name: "5", options: normalizedServiceOptions(defaultServiceOptions)})

	assert.Equal(t, "1", sm.ServiceForHost("example.com").name)
	assert.Equal(t, "2", sm.ServiceForHost("app.example.com").name)
	assert.Equal(t, "3", sm.ServiceForHost("api.example.com").name)
	assert.Equal(t, "4", sm.ServiceForHost("anything.example.com").name)

	assert.Equal(t, "5", sm.ServiceForHost("extra.level.example.com").name)
	assert.Equal(t, "5", sm.ServiceForHost("other.com").name)

	sm = NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}})})

	assert.Nil(t, sm.ServiceForHost("app.example.com"))
}

func TestServiceMap_ServiceForRequest(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}, PathPrefixes: []string{"/api"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}, PathPrefixes: []string{"/api/special"}})})
	sm.Set(&Service{name: "4", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"other.example.com"}, PathPrefixes: []string{"/api"}})})
	sm.Set(&Service{name: "5", options: normalizedServiceOptions(ServiceOptions{PathPrefixes: []string{"/api"}})})
	sm.Set(&Service{name: "6", options: normalizedServiceOptions(defaultServiceOptions)})

	checkService := func(expected string, url string) {
		servivce, _ := sm.ServiceForRequest(httptest.NewRequest(http.MethodGet, url, nil))
		assert.Equal(t, expected, servivce.name)
	}

	checkService("1", "http://example.com/")
	checkService("1", "http://example.com/random")
	checkService("1", "http://example.com/apiary")
	checkService("2", "http://example.com/api")
	checkService("3", "http://example.com/api/special")
	checkService("4", "http://other.example.com/api/test")
	checkService("5", "http://second.example.com/api/test")
	checkService("6", "http://second.example.com/non-api/test")
}

func TestServiceMap_RouteForRequestUsesCoveringRootServiceClientCA(t *testing.T) {
	sm := NewServiceMap()
	clientCAs := map[string]*ClientCA{}
	deploy := func(name string, options ServiceOptions) {
		clientCAs[name] = &ClientCA{}
		sm.Set(&Service{name: name, options: normalizedServiceOptions(options), clientCA: clientCAs[name]})
	}

	deploy("root", ServiceOptions{Hosts: []string{"example.com"}, TLSRedirect: true})
	deploy("api", ServiceOptions{Hosts: []string{"example.com"}, PathPrefixes: []string{"/api"}})
	deploy("wildcard", ServiceOptions{Hosts: []string{"*.example.com"}, PathPrefixes: []string{"/", "/admin"}})
	deploy("path-only", ServiceOptions{Hosts: []string{"path.example.com"}, PathPrefixes: []string{"/api"}})
	deploy("org", ServiceOptions{Hosts: []string{"example.org"}})
	deploy("path-only-wildcard", ServiceOptions{Hosts: []string{"*.example.net"}, PathPrefixes: []string{"/api"}})

	checkClientCA := func(expectedService string, url string) {
		t.Helper()
		requirement := sm.RouteForRequest(httptest.NewRequest(http.MethodGet, url, nil)).clientCertificateRequirement

		if expectedService == "" {
			assert.Nil(t, requirement.clientCA)
		} else {
			assert.Same(t, clientCAs[expectedService], requirement.clientCA)
		}
	}

	checkClientCA("root", "http://example.com/")
	checkClientCA("root", "http://example.com/api/items")
	checkClientCA("root", "http://example.com:8080/api")
	checkClientCA("wildcard", "http://app.example.com/admin")
	checkClientCA("wildcard", "http://path.example.com/api")
	checkClientCA("wildcard", "http://path.example.com/other")
	checkClientCA("org", "http://example.org/")
	checkClientCA("", "http://unknown.org/")
	checkClientCA("", "http://app.example.net/api")

	assert.True(t, sm.RouteForRequest(httptest.NewRequest(http.MethodGet, "http://example.com/api", nil)).clientCertificateRequirement.tlsRedirect)
	assert.False(t, sm.RouteForRequest(httptest.NewRequest(http.MethodGet, "http://example.org/", nil)).clientCertificateRequirement.tlsRedirect)

	deploy("catch-all", defaultServiceOptions)

	checkClientCA("catch-all", "http://app.example.net/api")
	checkClientCA("catch-all", "http://unknown.org/")
	checkClientCA("wildcard", "http://path.example.com/api")
}

func TestServiceMap_CheckAvailability(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}, PathPrefixes: []string{"/api"}})})

	assert.Nil(t, sm.CheckAvailability("2", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})))

	assert.Nil(t, sm.CheckAvailability("4", normalizedServiceOptions(ServiceOptions{Hosts: []string{"api.example.com"}})))
	assert.Nil(t, sm.CheckAvailability("4", normalizedServiceOptions(ServiceOptions{Hosts: []string{""}})))
	assert.Nil(t, sm.CheckAvailability("4", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}, PathPrefixes: []string{"/app"}})))
	assert.Nil(t, sm.CheckAvailability("3", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}, PathPrefixes: []string{"/api"}})))

	assert.Equal(t, "2", sm.CheckAvailability("4", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})).name)
	assert.Equal(t, "3", sm.CheckAvailability("4", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}, PathPrefixes: []string{"/api"}})).name)
}

func TestServiceMap_DefaultTLSHostname(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})})
	assert.Empty(t, sm.DefaultTLSHostname())

	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true})})
	assert.Equal(t, "example.com", sm.DefaultTLSHostname())
}

func TestServiceMap_DefaultTLSHostnameIgnoresOnDemandTLSServices(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{TLSEnabled: true, TLSOnDemandURL: "/allow-host"})})
	assert.Empty(t, sm.DefaultTLSHostname())

	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true})})
	assert.Equal(t, "example.com", sm.DefaultTLSHostname())

	// Re-setting the on-demand service must not displace the default hostname.
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{TLSEnabled: true, TLSOnDemandURL: "/allow-host"})})
	assert.Equal(t, "example.com", sm.DefaultTLSHostname())
}

func TestServiceMap_SyncingTLSSettingsFromRootPath(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"1.example.com"}, TLSEnabled: true, TLSRedirect: false})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"1.example.com"}, PathPrefixes: []string{"/api"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"2.example.com"}, TLSEnabled: false, TLSRedirect: true})})
	sm.Set(&Service{name: "4", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"2.example.com"}, PathPrefixes: []string{"/api"}})})

	assert.True(t, sm.Get("1").options.TLSEnabled)
	assert.False(t, sm.Get("1").options.TLSRedirect)
	assert.True(t, sm.Get("2").options.TLSEnabled)
	assert.False(t, sm.Get("2").options.TLSRedirect)

	assert.False(t, sm.Get("3").options.TLSEnabled)
	assert.True(t, sm.Get("3").options.TLSRedirect)
	assert.False(t, sm.Get("4").options.TLSEnabled)
	assert.True(t, sm.Get("4").options.TLSRedirect)

	sm.Remove("1")

	assert.False(t, sm.Get("2").options.TLSEnabled)
	assert.True(t, sm.Get("2").options.TLSRedirect)
}

func TestServiceMap_CheckHostAvailability_EmptyHostsFirst(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(defaultServiceOptions)})

	assert.Nil(t, sm.CheckAvailability("2", normalizedServiceOptions(ServiceOptions{Hosts: []string{"app.example.com"}})))
}

func TestServiceMap_HostBypassingClientCAAfterSet(t *testing.T) {
	clientCARoot := func(hosts ...string) ServiceOptions {
		return normalizedServiceOptions(ServiceOptions{Hosts: hosts, TLSEnabled: true, TLSClientCAPath: "ca.pem"})
	}
	root := func(hosts ...string) ServiceOptions {
		return normalizedServiceOptions(ServiceOptions{Hosts: hosts, TLSEnabled: true})
	}
	pathOnly := func(hosts ...string) ServiceOptions {
		return normalizedServiceOptions(ServiceOptions{Hosts: hosts, PathPrefixes: []string{"/api"}})
	}
	serviceMap := func(services ...ServiceOptions) *ServiceMap {
		sm := NewServiceMap()
		for i, options := range services {
			sm.Set(&Service{name: fmt.Sprintf("existing-%d", i), options: options})
		}
		return sm
	}

	t.Run("path-only host covered by a wildcard client CA host", func(t *testing.T) {
		sm := serviceMap(clientCARoot("*.example.com"))
		assert.Equal(t, "app.example.com", sm.HostBypassingClientCAAfterSet("api", pathOnly("app.example.com")))
	})

	t.Run("path-only host covered by a catch-all client CA service", func(t *testing.T) {
		sm := serviceMap(normalizedServiceOptions(ServiceOptions{TLSEnabled: true, TLSOnDemandURL: "/check", TLSClientCAPath: "ca.pem"}))
		assert.Equal(t, "app.example.com", sm.HostBypassingClientCAAfterSet("api", pathOnly("app.example.com")))
		assert.Equal(t, "*.example.com", sm.HostBypassingClientCAAfterSet("api", pathOnly("*.example.com")))
	})

	t.Run("wildcard client CA host deployed after a path-only host it covers", func(t *testing.T) {
		sm := serviceMap(pathOnly("app.example.com"))
		assert.Equal(t, "app.example.com", sm.HostBypassingClientCAAfterSet("mtls", clientCARoot("*.example.com")))
	})

	t.Run("path-only host with its own root service", func(t *testing.T) {
		sm := serviceMap(clientCARoot("*.example.com"), root("app.example.com"))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("app.example.com")))
	})

	t.Run("root service replacing the path-only one", func(t *testing.T) {
		sm := serviceMap(clientCARoot("*.example.com"))
		sm.Set(&Service{name: "app", options: pathOnly("app.example.com")})
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("app", root("app.example.com")))
	})

	t.Run("path-only host covered by a wildcard host without client CA", func(t *testing.T) {
		sm := serviceMap(root("*.example.com"))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("app.example.com")))
	})

	t.Run("path-only host not covered by the wildcard host", func(t *testing.T) {
		sm := serviceMap(clientCARoot("*.example.com"))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("example.com")))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("app.other.example.com")))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("app.example.org")))
	})

	t.Run("path service on the wildcard client CA host itself", func(t *testing.T) {
		sm := serviceMap(clientCARoot("*.example.com"))
		assert.Empty(t, sm.HostBypassingClientCAAfterSet("api", pathOnly("*.example.com")))
	})
}

func BenchmarkServiceMap_SingleServiceRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(defaultServiceOptions)})

	b.Run("exact match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://one.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})
}

func BenchmarkServiceMap_WilcardRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"one.example.com"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"*.two.example.com"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(defaultServiceOptions)})

	b.Run("exact match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://one.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})

	b.Run("wildcard match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://anything.two.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})

	b.Run("default match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://missing.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})
}

func BenchmarkServiceMap_HostAndPathBasedRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"one.example.com"}, PathPrefixes: []string{"/api"}})})
	sm.Set(&Service{name: "2", options: normalizedServiceOptions(ServiceOptions{Hosts: []string{"one.example.com"}})})
	sm.Set(&Service{name: "3", options: normalizedServiceOptions(ServiceOptions{PathPrefixes: []string{"/app"}})})
	sm.Set(&Service{name: "4", options: normalizedServiceOptions(defaultServiceOptions)})

	b.Run("host and path match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://one.example.com/api", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})

	b.Run("host and default path", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://one.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})

	b.Run("path match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://example.com/app", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})

	b.Run("default", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})
}

// Helpers

func normalizedServiceOptions(so ServiceOptions) ServiceOptions {
	so.Normalize()
	return so
}
