package server

import (
	"path"
	"time"
)

const (
	DefaultDrainTimeout = time.Second * 5
	DefaultAddTimeout   = time.Second * 5
)

type Config struct {
	ListenPort         int
	ConfigDir          string
	AddTimeout         time.Duration
	DrainTimeout       time.Duration
	MaxRequestBodySize int
}

func (c Config) SocketPath() string {
	return path.Join(c.ConfigDir, "mproxy.sock")
}

func (c Config) StatePath() string {
	return path.Join(c.ConfigDir, "mproxy.state")
}
