package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/rpc"
	"sync"
)

var registered sync.Once

type ServiceManager interface {
	Add(hosts Hosts, waitForHealthy bool) error
	Remove(hosts Hosts) error
	Deploy(hosts Hosts) error
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
		slog.Error("Failed to register RPC handler", "error", err)
		return err
	}

	h.rpcListener, err = net.Listen("unix", socketPath)
	if err != nil {
		slog.Error("Failed to start RPC listener", "error", err)
		return err
	}

	go func() {
		for {
			conn, err := h.rpcListener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					slog.Debug("Closing RPC listener")
					return
				} else {
					slog.Error("Error accepting RPC connection", "error", err)
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

func (h *CommandHandler) AddHosts(hosts Hosts, reply *bool) error {
	err := h.serviceManager.Add(hosts, true)
	*reply = (err == nil)
	return err
}

func (h *CommandHandler) RemoveHosts(hosts Hosts, reply *bool) error {
	err := h.serviceManager.Remove(hosts)
	*reply = (err == nil)
	return err
}

func (h *CommandHandler) DeployHosts(hosts Hosts, reply *bool) error {
	err := h.serviceManager.Deploy(hosts)
	*reply = (err == nil)
	return err
}
