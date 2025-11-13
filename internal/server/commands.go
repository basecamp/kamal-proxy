package server

import (
	"errors"
	"log/slog"
	"net"
	"net/rpc"
	"sync"
	"time"
)

var registered sync.Once

type CommandHandler struct {
	rpcListener net.Listener
	router      *Router
}

type DeployArgs struct {
	Service           string
	TargetURLs        []string
	ReaderURLs        []string
	DeploymentOptions DeploymentOptions
	ServiceOptions    ServiceOptions
	TargetOptions     TargetOptions
}

type PauseArgs struct {
	Service      string
	DrainTimeout time.Duration
	PauseTimeout time.Duration
}

type StopArgs struct {
	Service      string
	DrainTimeout time.Duration
	Message      string
}

type ResumeArgs struct {
	Service string
}

type RemoveArgs struct {
	Service string
}

type RolloutDeployArgs struct {
	Service           string
	TargetURLs        []string
	ReaderURLs        []string
	DeploymentOptions DeploymentOptions
}

type RolloutSetArgs struct {
	Service    string
	Percentage int
	Allowlist  []string
}

type RolloutStopArgs struct {
	Service string
}

type ListResponse struct {
	Targets ServiceDescriptionMap `json:"services"`
}

func NewCommandHandler(router *Router) *CommandHandler {
	return &CommandHandler{
		router: router,
	}
}

func (h *CommandHandler) Start(socketPath string) error {
	var err error
	registered.Do(func() {
		err = rpc.RegisterName("kamal-proxy", h)
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

func (h *CommandHandler) Close() error {
	return h.rpcListener.Close()
}

func (h *CommandHandler) Deploy(args DeployArgs, reply *bool) error {
	return h.router.DeployService(args.Service, args.TargetURLs, args.ReaderURLs, args.ServiceOptions, args.TargetOptions, args.DeploymentOptions)
}

func (h *CommandHandler) Pause(args PauseArgs, reply *bool) error {
	return h.router.PauseService(args.Service, args.DrainTimeout, args.PauseTimeout)
}

func (h *CommandHandler) Stop(args StopArgs, reply *bool) error {
	return h.router.StopService(args.Service, args.DrainTimeout, args.Message)
}

func (h *CommandHandler) Resume(args ResumeArgs, reply *bool) error {
	return h.router.ResumeService(args.Service)
}

func (h *CommandHandler) Remove(args RemoveArgs, reply *bool) error {
	return h.router.RemoveService(args.Service)
}

func (h *CommandHandler) List(args bool, reply *ListResponse) error {
	reply.Targets = h.router.ListActiveServices()

	return nil
}

func (h *CommandHandler) RolloutDeploy(args RolloutDeployArgs, reply *bool) error {
	return h.router.SetRolloutTargets(args.Service, args.TargetURLs, args.ReaderURLs, args.DeploymentOptions)
}

func (h *CommandHandler) RolloutSet(args RolloutSetArgs, reply *bool) error {
	return h.router.SetRolloutSplit(args.Service, args.Percentage, args.Allowlist)
}

func (h *CommandHandler) RolloutStop(args RolloutStopArgs, reply *bool) error {
	return h.router.StopRollout(args.Service)
}
