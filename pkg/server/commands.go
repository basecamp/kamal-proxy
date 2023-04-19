package server

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"net/url"
	"sync"

	"github.com/rs/zerolog/log"
)

var registered sync.Once

type ServiceManager interface {
	Add(hostURLs []*url.URL, waitForHealthy bool) error
	Remove(hostURLs []*url.URL) error
	GetServices() []*Service
}

type CommandHandler struct {
	serviceManager ServiceManager
	rpcListener    net.Listener
}

type ListResponse struct {
	Hosts []string
}

func NewCommandHandler(serviceManager ServiceManager) *CommandHandler {
	return &CommandHandler{
		serviceManager: serviceManager,
	}
}

func (h *CommandHandler) Start(socketPath string) error {
	var err error
	registered.Do(func() {
		err = rpc.RegisterName("mproxy", h)
	})
	if err != nil {
		log.Err(err).Msg("Failed to register RPC handler")
		return err
	}

	h.rpcListener, err = net.Listen("unix", socketPath)
	if err != nil {
		log.Err(err).Msg("Failed to start RPC listener")
		return err
	}

	go func() {
		for {
			conn, err := h.rpcListener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					log.Debug().Msg("Closing RPC listener")
					return
				} else {
					log.Err(err).Msg("Error accepting RPC connection")
					continue
				}
			}

			go rpc.ServeConn(conn)
		}
	}()

	return nil
}

func (h *CommandHandler) Stop() error {
	return h.rpcListener.Close()
}

func (h *CommandHandler) List(_ bool, reply *ListResponse) error {
	services := h.serviceManager.GetServices()

	reply.Hosts = []string{}
	for _, service := range services {
		reply.Hosts = append(reply.Hosts, fmt.Sprintf("%-24s (%s)", service.hostURL.Host, service.state))
	}

	return nil
}

func (h *CommandHandler) AddHosts(hostURLs []*url.URL, reply *bool) error {
	err := h.serviceManager.Add(hostURLs, true)
	*reply = (err == nil)
	return err
}

func (h *CommandHandler) RemoveHosts(hostURLs []*url.URL, reply *bool) error {
	err := h.serviceManager.Remove(hostURLs)
	*reply = (err == nil)
	return err
}
