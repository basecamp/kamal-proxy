package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostServiceMap_ServiceForHost(t *testing.T) {
	hsm := HostServiceMap{
		"example.com":     &Service{name: "1"},
		"app.example.com": &Service{name: "2"},
		"api.example.com": &Service{name: "3"},
		"*.example.com":   &Service{name: "4"},
		"":                &Service{name: "5"},
	}

	assert.Equal(t, "1", hsm.ServiceForHost("example.com").name)
	assert.Equal(t, "2", hsm.ServiceForHost("app.example.com").name)
	assert.Equal(t, "3", hsm.ServiceForHost("api.example.com").name)
	assert.Equal(t, "4", hsm.ServiceForHost("anything.example.com").name)

	assert.Equal(t, "5", hsm.ServiceForHost("extra.level.example.com").name)
	assert.Equal(t, "5", hsm.ServiceForHost("other.com").name)

	hsm = HostServiceMap{
		"example.com": &Service{name: "1"},
	}

	assert.Nil(t, hsm.ServiceForHost("app.example.com"))
}

func BenchmarkHostServiceMap_WilcardRouting(b *testing.B) {
	hsm := HostServiceMap{
		"one.example.com":   &Service{},
		"*.two.example.com": &Service{},
		"":                  &Service{},
	}

	b.Run("exact match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("one.example.com")
		}
	})

	b.Run("wildcard match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("anything.two.example.com")
		}
	})

	b.Run("default match", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = hsm.ServiceForHost("missing.example.com")
		}
	})
}
