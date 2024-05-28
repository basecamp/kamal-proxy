package server

import (
	"path"
)

const (
	DefaultHttpPort  = 80
	DefaultHttpsPort = 443
)

type Config struct {
	Bind      string
	ConfigDir string
	HttpPort  int
	HttpsPort int
}

func (c Config) SocketPath() string {
	return path.Join(c.ConfigDir, "kamal-proxy.sock")
}

func (c Config) StatePath() string {
	return path.Join(c.ConfigDir, "kamal-proxy.state")
}

func (c Config) CertificatePath() string {
	return path.Join(c.ConfigDir, "certs")
}
