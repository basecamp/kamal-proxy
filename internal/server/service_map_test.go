package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceMap_ServiceForHost(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"app.example.com"}})
	sm.Set(&Service{name: "3", hosts: []string{"api.example.com"}})
	sm.Set(&Service{name: "4", hosts: []string{"*.example.com"}})
	sm.Set(&Service{name: "5"})

	assert.Equal(t, "1", sm.ServiceForHost("example.com").name)
	assert.Equal(t, "2", sm.ServiceForHost("app.example.com").name)
	assert.Equal(t, "3", sm.ServiceForHost("api.example.com").name)
	assert.Equal(t, "4", sm.ServiceForHost("anything.example.com").name)

	assert.Equal(t, "5", sm.ServiceForHost("extra.level.example.com").name)
	assert.Equal(t, "5", sm.ServiceForHost("other.com").name)

	sm = NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"example.com"}})

	assert.Nil(t, sm.ServiceForHost("app.example.com"))
}

func TestServiceMap_ServiceForRequest(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"example.com"}, pathPrefix: "/api"})
	sm.Set(&Service{name: "3", hosts: []string{"example.com"}, pathPrefix: "/api/special"})
	sm.Set(&Service{name: "4", hosts: []string{"other.example.com"}, pathPrefix: "/api"})
	sm.Set(&Service{name: "5", pathPrefix: "/api"})
	sm.Set(&Service{name: "6"})

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

func TestServiceMap_CheckAvailability(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"app.example.com"}})
	sm.Set(&Service{name: "3", hosts: []string{"app.example.com"}, pathPrefix: "/api"})

	assert.Nil(t, sm.CheckAvailability("2", []string{"app.example.com"}, ""))

	assert.Nil(t, sm.CheckAvailability("4", []string{"api.example.com"}, ""))
	assert.Nil(t, sm.CheckAvailability("4", []string{""}, ""))
	assert.Nil(t, sm.CheckAvailability("4", []string{"app.example.com"}, "/app"))
	assert.Nil(t, sm.CheckAvailability("3", []string{"app.example.com"}, "/api"))

	assert.Equal(t, "2", sm.CheckAvailability("4", []string{"app.example.com"}, "").name)
	assert.Equal(t, "3", sm.CheckAvailability("4", []string{"app.example.com"}, "/api").name)
}

func TestServiceMap_SyncingTLSSettingsFromRootPath(t *testing.T) {
	optionsWithTLS := ServiceOptions{
		TLSEnabled:         true,
		TLSDisableRedirect: true,
	}

	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"1.example.com"}, options: optionsWithTLS})
	sm.Set(&Service{name: "2", hosts: []string{"1.example.com"}, pathPrefix: "/api"})
	sm.Set(&Service{name: "3", hosts: []string{"2.example.com"}, options: defaultServiceOptions})
	sm.Set(&Service{name: "4", hosts: []string{"2.example.com"}, pathPrefix: "/api"})

	assert.True(t, sm.Get("1").options.TLSEnabled)
	assert.True(t, sm.Get("1").options.TLSDisableRedirect)
	assert.True(t, sm.Get("2").options.TLSEnabled)
	assert.True(t, sm.Get("2").options.TLSDisableRedirect)

	assert.False(t, sm.Get("3").options.TLSEnabled)
	assert.False(t, sm.Get("3").options.TLSDisableRedirect)
	assert.False(t, sm.Get("4").options.TLSEnabled)
	assert.False(t, sm.Get("4").options.TLSDisableRedirect)

	sm.Remove("1")

	assert.False(t, sm.Get("2").options.TLSEnabled)
	assert.False(t, sm.Get("2").options.TLSDisableRedirect)
}

func TestServiceMap_CheckHostAvailability_EmptyHostsFirst(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{}})

	assert.Nil(t, sm.CheckAvailability("2", []string{"app.example.com"}, ""))
}

func BenchmarkServiceMap_SingleServiceRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1"})

	b.Run("exact match", func(b *testing.B) {
		req := httptest.NewRequest(http.MethodGet, "https://one.example.com/", nil)

		for b.Loop() {
			_, _ = sm.ServiceForRequest(req)
		}
	})
}

func BenchmarkServiceMap_WilcardRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"one.example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"*.two.example.com"}})
	sm.Set(&Service{name: "3"})

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
	sm.Set(&Service{name: "1", hosts: []string{"one.example.com"}, pathPrefix: "/api"})
	sm.Set(&Service{name: "2", hosts: []string{"one.example.com"}})
	sm.Set(&Service{name: "3", pathPrefix: "/app"})
	sm.Set(&Service{name: "4"})

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
