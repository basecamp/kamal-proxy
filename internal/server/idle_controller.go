package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type IdleState int

const (
	IdleStateActive IdleState = iota
	IdleStateStopping
	IdleStateSleeping
	IdleStateWaking
)

func (s IdleState) String() string {
	switch s {
	case IdleStateActive:
		return "active"
	case IdleStateStopping:
		return "stopping"
	case IdleStateSleeping:
		return "sleeping"
	case IdleStateWaking:
		return "waking"
	default:
		return ""
	}
}

var ErrIdleWakeTimeout = errors.New("idle container wake timed out")

type ContainerLifecycle interface {
	StartContainer(context.Context, string) error
	StopContainer(context.Context, string) error
}

type IdleController struct {
	State          IdleState     `json:"state"`
	IdleTimeout    time.Duration `json:"idle_timeout"`
	WakeTimeout    time.Duration `json:"wake_timeout"`
	ContainerNames []string      `json:"container_names"`

	mu              sync.Mutex
	lifecycle       ContainerLifecycle
	ready           func(time.Duration) error
	inflight        int
	lastRequest     time.Time
	wakeDone        chan struct{}
	wakeErr         error
	lifecycleCancel context.CancelFunc
	changed         chan struct{}
	closed          chan struct{}
	disabled        bool
	closeOnce       sync.Once
	persist         func()
}

func NewIdleController(idleTimeout, wakeTimeout time.Duration, names []string, lifecycle ContainerLifecycle, ready func(time.Duration) error) *IdleController {
	c := &IdleController{State: IdleStateActive}
	c.configure(idleTimeout, wakeTimeout, names, lifecycle, ready)
	return c
}

func (c *IdleController) MarshalJSON() ([]byte, error) {
	type persisted struct {
		State          IdleState     `json:"state"`
		IdleTimeout    time.Duration `json:"idle_timeout"`
		WakeTimeout    time.Duration `json:"wake_timeout"`
		ContainerNames []string      `json:"container_names"`
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return json.Marshal(persisted{
		State:          c.State,
		IdleTimeout:    c.IdleTimeout,
		WakeTimeout:    c.WakeTimeout,
		ContainerNames: c.ContainerNames,
	})
}

func (c *IdleController) UnmarshalJSON(data []byte) error {
	type persisted struct {
		State          IdleState     `json:"state"`
		IdleTimeout    time.Duration `json:"idle_timeout"`
		WakeTimeout    time.Duration `json:"wake_timeout"`
		ContainerNames []string      `json:"container_names"`
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c.State, c.IdleTimeout, c.WakeTimeout, c.ContainerNames = p.State, p.IdleTimeout, p.WakeTimeout, p.ContainerNames
	if c.State == IdleStateWaking || c.State == IdleStateStopping {
		c.State = IdleStateSleeping
	}
	return nil
}

func (c *IdleController) configure(idleTimeout, wakeTimeout time.Duration, names []string, lifecycle ContainerLifecycle, ready func(time.Duration) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.IdleTimeout, c.WakeTimeout = idleTimeout, wakeTimeout
	c.ContainerNames = append([]string(nil), names...)
	c.lifecycle, c.ready = lifecycle, ready
	c.lastRequest = time.Now()
	if c.changed == nil {
		c.changed, c.closed = make(chan struct{}, 1), make(chan struct{})
		go c.run()
	}
	c.signal()
}

func (c *IdleController) BeginRequest(ctx context.Context) error {
	c.mu.Lock()
	c.inflight++
	c.lastRequest = time.Now()
	c.mu.Unlock()
	c.signal()
	for {
		c.mu.Lock()
		if c.State == IdleStateSleeping && !c.disabled {
			c.startWakeLocked()
		}
		done, timeout, state := c.wakeDone, c.WakeTimeout, c.State
		c.mu.Unlock()
		if state != IdleStateWaking && state != IdleStateStopping {
			return nil
		}
		timer := time.NewTimer(timeout)
		select {
		case <-done:
			timer.Stop()
			if state == IdleStateStopping {
				continue
			}
			c.mu.Lock()
			err := c.wakeErr
			c.mu.Unlock()
			return err
		case <-timer.C:
			return ErrIdleWakeTimeout
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (c *IdleController) EndRequest() {
	c.mu.Lock()
	c.inflight--
	c.lastRequest = time.Now()
	c.mu.Unlock()
	c.signal()
}

func (c *IdleController) StateValue() IdleState { c.mu.Lock(); defer c.mu.Unlock(); return c.State }

func (c *IdleController) SetPersist(fn func()) { c.mu.Lock(); c.persist = fn; c.mu.Unlock() }

func (c *IdleController) notifyPersist() {
	c.mu.Lock()
	fn := c.persist
	c.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

func (c *IdleController) Disable() {
	c.mu.Lock()
	c.disabled = true
	if c.State == IdleStateWaking || c.State == IdleStateStopping {
		c.cancelLifecycleLocked()
		c.State = IdleStateSleeping
	}
	c.mu.Unlock()
	c.signal()
}
func (c *IdleController) Enable() {
	c.mu.Lock()
	c.disabled = false
	c.lastRequest = time.Now()
	c.mu.Unlock()
	c.signal()
}

func (c *IdleController) Reset(names []string, ready func(time.Duration) error) {
	c.mu.Lock()
	c.ContainerNames = append([]string(nil), names...)
	c.ready = ready
	c.State, c.wakeErr, c.lastRequest = IdleStateActive, nil, time.Now()
	c.cancelLifecycleLocked()
	c.mu.Unlock()
	c.signal()
}

func (c *IdleController) cancelLifecycleLocked() {
	if c.lifecycleCancel != nil {
		c.lifecycleCancel()
		c.lifecycleCancel = nil
	}
	if c.wakeDone != nil {
		select {
		case <-c.wakeDone:
		default:
			close(c.wakeDone)
		}
		c.wakeDone = nil
	}
}

func (c *IdleController) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.cancelLifecycleLocked()
		c.mu.Unlock()
		if c.closed != nil {
			close(c.closed)
		}
	})
}
func (c *IdleController) signal() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

func (c *IdleController) run() {
	for {
		c.mu.Lock()
		wait := c.IdleTimeout - time.Since(c.lastRequest)
		eligible := !c.disabled && c.State == IdleStateActive && c.inflight == 0 && c.IdleTimeout > 0
		c.mu.Unlock()
		if !eligible {
			wait = time.Hour
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
			c.trySleep()
		case <-c.changed:
			if !timer.Stop() {
				<-timer.C
			}
		case <-c.closed:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (c *IdleController) trySleep() {
	c.mu.Lock()
	if c.disabled || c.State != IdleStateActive || c.inflight != 0 || c.IdleTimeout <= 0 || time.Since(c.lastRequest) < c.IdleTimeout {
		c.mu.Unlock()
		return
	}
	names, lifecycle := append([]string(nil), c.ContainerNames...), c.lifecycle
	c.State, c.wakeDone = IdleStateStopping, make(chan struct{})
	stopDone := c.wakeDone
	ctx, cancel := context.WithTimeout(context.Background(), DefaultIdleLifecycleTimeout)
	c.lifecycleCancel = cancel
	c.mu.Unlock()
	defer cancel()
	for _, name := range names {
		if err := lifecycle.StopContainer(ctx, name); err != nil {
			slog.Error("Failed to stop idle container", "container", name, "error", err)
			c.mu.Lock()
			if c.wakeDone == stopDone {
				c.State, c.lastRequest = IdleStateActive, time.Now()
				c.lifecycleCancel = nil
				close(stopDone)
			}
			c.mu.Unlock()
			c.notifyPersist()
			return
		}
	}
	c.mu.Lock()
	if c.wakeDone == stopDone {
		c.State = IdleStateSleeping
		c.lifecycleCancel = nil
		close(stopDone)
	}
	c.mu.Unlock()
	c.notifyPersist()
	c.signal()
}

func (c *IdleController) startWakeLocked() {
	c.State, c.wakeErr, c.wakeDone = IdleStateWaking, nil, make(chan struct{})
	done, names, timeout, lifecycle, ready := c.wakeDone, append([]string(nil), c.ContainerNames...), c.WakeTimeout, c.lifecycle, c.ready
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	c.lifecycleCancel = cancel
	go func() {
		defer cancel()
		var err error
		for _, name := range names {
			if ctx.Err() != nil {
				err = ctx.Err()
				break
			}
			if err = lifecycle.StartContainer(ctx, name); err != nil {
				break
			}
		}
		if err == nil {
			c.mu.Lock()
			current := c.wakeDone == done
			c.mu.Unlock()
			if current {
				err = ready(timeout)
			}
		}
		c.mu.Lock()
		if c.wakeDone == done {
			c.wakeErr = err
			c.lifecycleCancel = nil
			if err == nil {
				c.State, c.lastRequest = IdleStateActive, time.Now()
			} else {
				c.State = IdleStateSleeping
			}
			close(done)
		}
		c.mu.Unlock()
		c.notifyPersist()
		c.signal()
	}()
}
