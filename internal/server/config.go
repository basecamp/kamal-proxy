package server

import (
	"cmp"
	"os"
	"os/user"
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
	return path.Join(c.RuntimeDirectory(), "kamal-proxy.sock")
}

func (c Config) StatePath() string {
	return path.Join(c.StateDirectory(), "kamal-proxy.state")
}

func (c Config) CertificatePath() string {
	return path.Join(c.DataDirectory(), "certs")
}

func (c Config) RuntimeDirectory() string {
	return c.locateDirectory("XDG_RUNTIME_DIR", "/tmp", "run")
}

func (c Config) StateDirectory() string {
	return c.locateDirectory("XDG_STATE_HOME", ".local/state", "state")
}

func (c Config) DataDirectory() string {
	return c.locateDirectory("XDG_DATA_HOME", ".local/share", "data")
}

func (c Config) locateDirectory(env, defaultDir, relocatedDir string) string {
	if defaultDir[0] != '/' {
		usr, _ := user.Current()
		defaultDir = path.Join(usr.HomeDir, defaultDir)
	}

	dir := cmp.Or(os.Getenv(env), defaultDir)
	if c.ConfigDir != "" {
		dir = path.Join(c.ConfigDir, relocatedDir)
	}

	dir = path.Join(dir, "kamal-proxy")
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		panic(err)
	}

	return dir
}
