package server

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrorAlreadyRunning = errors.New("already running")
	ErrorAlreadyPaused  = errors.New("already paused")
	ErrorAlreadyStopped = errors.New("already stopped")
)

type PauseState int

const (
	PauseStateRunning PauseState = iota
	PauseStatePaused
	PauseStateStopped
)

type PauseWaitAction int

const (
	PauseWaitActionProceed PauseWaitAction = iota
	PauseWaitActionTimedOut
	PauseWaitActionUnavailable
)

type PauseControl struct {
	lock         sync.RWMutex
	state        PauseState
	pauseChannel chan bool
	failAfter    time.Duration
}

func NewPauseControl() *PauseControl {
	return &PauseControl{}
}

func (p *PauseControl) State() PauseState {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.state
}

func (p *PauseControl) Stop() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state == PauseStateStopped {
		return ErrorAlreadyStopped
	}

	if p.state == PauseStatePaused {
		close(p.pauseChannel)
	}

	p.state = PauseStateStopped

	return nil
}

func (p *PauseControl) Pause(failAfter time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state == PauseStatePaused {
		return ErrorAlreadyPaused
	}

	p.state = PauseStatePaused
	p.pauseChannel = make(chan bool)
	p.failAfter = failAfter

	return nil
}

func (p *PauseControl) Resume() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state == PauseStateRunning {
		return ErrorAlreadyRunning
	}

	if p.state == PauseStatePaused {
		close(p.pauseChannel)
	}
	p.state = PauseStateRunning

	return nil
}

func (p *PauseControl) Wait() PauseWaitAction {
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

func (p *PauseControl) getWaitState() (PauseState, chan bool, <-chan time.Time) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.state == PauseStatePaused {
		return PauseStatePaused, p.pauseChannel, time.After(p.failAfter)
	}

	return p.state, nil, nil
}
