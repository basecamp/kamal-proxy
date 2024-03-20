package server

import (
	"path"
)

const (
	DefaultHttpPort  = 80
	DefaultHttpsPort = 443
)

type Config struct {
	ConfigDir string
	HttpPort  int
	HttpsPort int
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
