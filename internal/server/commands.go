package server

import (
	"crypto/rand"
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
	drains      sync.Map
}

type pendingDrain struct {
	done chan struct{}
	err  error
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

type RolloutEnableArgs struct {
	Service string
}

type RolloutDisableArgs struct {
	Service string
}

type RolloutRemoveArgs struct {
	Service      string
	DrainTimeout time.Duration
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

// DeployAsyncDrain returns once the healthy targets have taken over new traffic.
// Draining continues even if the deploying client disconnects or its hook fails.
func (h *CommandHandler) DeployAsyncDrain(args DeployArgs, reply *string) error {
	id := rand.Text()
	drain := &pendingDrain{done: make(chan struct{})}
	h.drains.Store(id, drain)
	started := make(chan struct{})

	go func() {
		drain.err = h.router.deployService(args.Service, args.TargetURLs, args.ReaderURLs, args.ServiceOptions, args.TargetOptions, args.DeploymentOptions, func() {
			close(started)
		})
		close(drain.done)
		// Allow the caller to wait after draining has finished, but expire results
		// if it never waits. WaitDrain consumes the result on the normal path.
		time.AfterFunc(5*time.Minute, func() { h.drains.Delete(id) })
	}()

	select {
	case <-started:
		*reply = id
		return nil
	case <-drain.done:
		if drain.err != nil {
			h.drains.Delete(id)
			return drain.err
		}
		*reply = id
		return nil
	}
}

func (h *CommandHandler) WaitDrain(id string, reply *bool) error {
	value, ok := h.drains.LoadAndDelete(id)
	if !ok {
		return errors.New("unknown drain")
	}
	drain := value.(*pendingDrain)
	<-drain.done
	return drain.err
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

func (h *CommandHandler) RolloutEnable(args RolloutEnableArgs, reply *bool) error {
	return h.router.EnableRollout(args.Service)
}

func (h *CommandHandler) RolloutDisable(args RolloutDisableArgs, reply *bool) error {
	return h.router.DisableRollout(args.Service)
}

func (h *CommandHandler) RolloutRemove(args RolloutRemoveArgs, reply *bool) error {
	return h.router.RemoveRolloutTargets(args.Service, args.DrainTimeout)
}
