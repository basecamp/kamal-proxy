package server

import "time"

const (
	DefaultDrainTimeout = time.Second * 5
	DefaultAddTimeout   = time.Second * 5
)

type Config struct {
	ListenPort         int
	SocketPath         string
	AddTimeout         time.Duration
	DrainTimeout       time.Duration
	MaxRequestBodySize int
}
