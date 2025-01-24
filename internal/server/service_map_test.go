package server

import (
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

func TestServiceMap_CheckHostAvailability(t *testing.T) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"app.example.com"}})

	assert.Nil(t, sm.CheckHostAvailability("2", []string{"app.example.com"}))
	assert.Nil(t, sm.CheckHostAvailability("3", []string{"api.example.com"}))
	assert.Nil(t, sm.CheckHostAvailability("4", []string{""}))

	assert.Equal(t, "2", sm.CheckHostAvailability("3", []string{"app.example.com"}).name)

	sm.Set(&Service{name: "3", hosts: []string{}})
	assert.Equal(t, "3", sm.CheckHostAvailability("4", []string{""}).name)
}

func BenchmarkServiceMap_WilcardRouting(b *testing.B) {
	sm := NewServiceMap()
	sm.Set(&Service{name: "1", hosts: []string{"one.example.com"}})
	sm.Set(&Service{name: "2", hosts: []string{"*.two.example.com"}})
	sm.Set(&Service{name: "3"})

	b.Run("exact match", func(b *testing.B) {
		for b.Loop() {
			_ = sm.ServiceForHost("one.example.com")
		}
	})

	b.Run("wildcard match", func(b *testing.B) {
		for b.Loop() {
			_ = sm.ServiceForHost("anything.two.example.com")
		}
	})

	b.Run("default match", func(b *testing.B) {
		for b.Loop() {
			_ = sm.ServiceForHost("missing.example.com")
		}
	})
}
