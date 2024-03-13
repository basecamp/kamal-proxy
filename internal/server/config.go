package server

import (
	"path"
	"time"
)

const (
	DefaultHttpPort           = 80
	DefaultHttpsPort          = 443
	DefaultHttpIdleTimeout    = 60 * time.Second
	DefaultHttpReadTimeout    = 30 * time.Second
	DefaultHttpWriteTimeout   = 30 * time.Second
	DefaultMaxRequestBodySize = 0
)

type Config struct {
	ConfigDir          string
	HttpPort           int
	HttpsPort          int
	HttpIdleTimeout    time.Duration
	HttpReadTimeout    time.Duration
	HttpWriteTimeout   time.Duration
	MaxRequestBodySize int64
}

func (c Config) SocketPath() string {
	return path.Join(c.ConfigDir, "mproxy.sock")
}

func (c Config) StatePath() string {
	return path.Join(c.ConfigDir, "mproxy.state")
}

func (c Config) CertificatePath() string {
	return path.Join(c.ConfigDir, "certs")
}
