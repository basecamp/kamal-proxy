package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

type IdleState int

const (
	IdleStateActive IdleState = iota
	IdleStateSleeping
	IdleStateWaking
)

func (is IdleState) String() string {
	switch is {
	case IdleStateActive:
		return "active"
	case IdleStateSleeping:
		return "sleeping"
	case IdleStateWaking:
		return "waking"
	default:
		return ""
	}
}

type IdleWaitAction int

const (
	IdleWaitActionProceed IdleWaitAction = iota
	IdleWaitActionTimedOut
)

type IdleController struct {
	State          IdleState     `json:"state"`
	IdleTimeout    time.Duration `json:"idle_timeout"`
	WakeTimeout    time.Duration `json:"wake_timeout"`
	ContainerNames []string      `json:"container_names"`

	serviceName string
	docker      *DockerClient
	lb          *LoadBalancer

	lock          sync.RWMutex
	lastRequestAt time.Time
	wakeChan      chan bool
	closeChan     chan bool
	disabled      bool
}

func NewIdleController(serviceName string, idleTimeout, wakeTimeout time.Duration, containerNames []string, docker *DockerClient, lb *LoadBalancer) *IdleController {
	ic := &IdleController{
		State:          IdleStateActive,
		IdleTimeout:    idleTimeout,
		WakeTimeout:    wakeTimeout,
		ContainerNames: containerNames,
		serviceName:    serviceName,
		docker:         docker,
		lb:             lb,
		lastRequestAt:  time.Now(),
		closeChan:      make(chan bool),
	}

	go ic.run()
	return ic
}

func (ic *IdleController) UnmarshalJSON(data []byte) error {
	type alias IdleController
	aux := &struct {
		*alias
	}{
		alias: (*alias)(ic),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	ic.lastRequestAt = time.Now()
	ic.closeChan = make(chan bool)

	go ic.run()
	return nil
}

func (ic *IdleController) TrackActivity() {
	ic.lock.Lock()
	defer ic.lock.Unlock()

	ic.lastRequestAt = time.Now()
}

func (ic *IdleController) GetState() IdleState {
	ic.lock.RLock()
	defer ic.lock.RUnlock()
	return ic.State
}

func (ic *IdleController) WaitIfSleeping() IdleWaitAction {
	ic.lock.RLock()
	state := ic.State
	wakeChan := ic.wakeChan
	wakeTimeout := ic.WakeTimeout
	ic.lock.RUnlock()

	if state == IdleStateActive {
		return IdleWaitActionProceed
	}

	if state == IdleStateSleeping {
		ic.wake()
		// Re-read wakeChan
		ic.lock.RLock()
		wakeChan = ic.wakeChan
		ic.lock.RUnlock()
	}

	if wakeChan == nil {
		return IdleWaitActionProceed
	}

	select {
	case <-wakeChan:
		return IdleWaitActionProceed
	case <-time.After(wakeTimeout):
		return IdleWaitActionTimedOut
	}
}

func (ic *IdleController) UpdateContainers(names []string) {
	ic.lock.Lock()
	defer ic.lock.Unlock()

	ic.ContainerNames = names
	ic.lastRequestAt = time.Now()
	
	if ic.State != IdleStateActive {
		ic.State = IdleStateActive
		if ic.wakeChan != nil {
			close(ic.wakeChan)
			ic.wakeChan = nil
		}
	}
}

func (ic *IdleController) Disable() {
	ic.lock.Lock()
	defer ic.lock.Unlock()
	ic.disabled = true
}

func (ic *IdleController) Enable() {
	ic.lock.Lock()
	defer ic.lock.Unlock()
	ic.disabled = false
	ic.lastRequestAt = time.Now()
}

func (ic *IdleController) Close() {
	close(ic.closeChan)
}

func (ic *IdleController) run() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ic.closeChan:
			return
		case <-ticker.C:
			ic.checkIdle()
		}
	}
}

func (ic *IdleController) checkIdle() {
	ic.lock.Lock()
	if ic.disabled || ic.State != IdleStateActive || ic.IdleTimeout <= 0 {
		ic.lock.Unlock()
		return
	}

	if time.Since(ic.lastRequestAt) > ic.IdleTimeout {
		ic.State = IdleStateSleeping
		ic.wakeChan = make(chan bool)
		containerNames := ic.ContainerNames
		ic.lock.Unlock()

		slog.Info("Service is idle, stopping containers", "service", ic.serviceName, "containers", containerNames)
		
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for _, name := range containerNames {
			if err := ic.docker.StopContainer(ctx, name); err != nil {
				slog.Error("Failed to stop idle container", "service", ic.serviceName, "container", name, "error", err)
			}
		}
	} else {
		ic.lock.Unlock()
	}
}

func (ic *IdleController) wake() {
	ic.lock.Lock()
	if ic.State != IdleStateSleeping {
		ic.lock.Unlock()
		return
	}

	ic.State = IdleStateWaking
	containerNames := ic.ContainerNames
	ic.lock.Unlock()

	slog.Info("Service waking up, starting containers", "service", ic.serviceName, "containers", containerNames)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ic.WakeTimeout)
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(len(containerNames))
		for _, name := range containerNames {
			go func(n string) {
				defer wg.Done()
				if err := ic.docker.StartContainer(ctx, n); err != nil {
					slog.Error("Failed to start container during wake", "service", ic.serviceName, "container", n, "error", err)
				}
			}(name)
		}
		wg.Wait()

		// Wait until healthy
		err := ic.lb.WaitUntilHealthy(ic.WakeTimeout)
		if err != nil {
			slog.Error("Service failed to become healthy after wake", "service", ic.serviceName, "error", err)
		}

		ic.lock.Lock()
		ic.State = IdleStateActive
		ic.lastRequestAt = time.Now()
		close(ic.wakeChan)
		ic.wakeChan = nil
		ic.lock.Unlock()
	}()
}
