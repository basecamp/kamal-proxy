package server

import (
	"path"
	"time"
)

const (
	DefaultAddTimeout   = time.Second * 5
	DefaultDrainTimeout = time.Second * 5

	DefaultHealthCheckPath     = "/up"
	DefaultHealthCheckInterval = time.Second
	DefaultHealthCheckTimeout  = time.Second * 5
)

type HealthCheckConfig struct {
	HealthCheckPath     string
	HealthCheckInterval time.Duration
	HealthCheckTimeout  time.Duration
}

type Config struct {
	ListenPort int
	ConfigDir  string
	Debug      bool

	AddTimeout         time.Duration
	DrainTimeout       time.Duration
	MaxRequestBodySize int64

	HealthCheckConfig
}

func (c Config) SocketPath() string {
	return path.Join(c.ConfigDir, "mproxy.sock")
}

func (c Config) StatePath() string {
	return path.Join(c.ConfigDir, "mproxy.state")
}
