package server

import (
	"sync"
	"time"
)

type PauseState int

const (
	PauseStateRunning PauseState = iota
	PauseStatePaused
	PauseStateStopped
)

func (ps PauseState) String() string {
	switch ps {
	case PauseStateRunning:
		return "running"
	case PauseStatePaused:
		return "paused"
	case PauseStateStopped:
		return "stopped"
	default:
		return ""
	}
}

type PauseWaitAction int

const (
	PauseWaitActionProceed PauseWaitAction = iota
	PauseWaitActionTimedOut
	PauseWaitActionUnavailable
)

type PauseController struct {
	lock         sync.RWMutex
	state        PauseState
	pauseChannel chan bool
	failAfter    time.Duration
}

func NewPauseController() *PauseController {
	return &PauseController{}
}

func (p *PauseController) State() PauseState {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.state
}

func (p *PauseController) Stop() error {
	p.setState(PauseStateStopped)
	return nil
}

func (p *PauseController) Pause(failAfter time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state != PauseStatePaused {
		p.pauseChannel = make(chan bool)
	}

	p.state = PauseStatePaused
	p.failAfter = failAfter
	return nil
}

func (p *PauseController) Resume() error {
	p.setState(PauseStateRunning)
	return nil
}

func (p *PauseController) Wait() PauseWaitAction {
	state, pauseChannel, failChannel := p.getWaitState()

	switch state {
	case PauseStateRunning:
		return PauseWaitActionProceed

	case PauseStateStopped:
		return PauseWaitActionUnavailable

	default:
		select {
		case <-pauseChannel:
			switch p.State() {
			case PauseStateStopped:
				return PauseWaitActionUnavailable
			default:
				return PauseWaitActionProceed
			}
		case <-failChannel:
			return PauseWaitActionTimedOut
		}
	}
}

func (p *PauseController) getWaitState() (PauseState, chan bool, <-chan time.Time) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.state == PauseStatePaused {
		return PauseStatePaused, p.pauseChannel, time.After(p.failAfter)
	}

	return p.state, nil, nil
}

func (p *PauseController) setState(newState PauseState) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state == newState {
		return
	}

	if p.state == PauseStatePaused {
		close(p.pauseChannel)
	}

	p.state = newState
}
