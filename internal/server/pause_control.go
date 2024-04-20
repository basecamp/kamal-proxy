package server

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrorAlreadyPaused = errors.New("already paused")
	ErrorNotPaused     = errors.New("not paused")
)

type PauseControl struct {
	lock         sync.RWMutex
	paused       bool
	pauseChannel chan bool
	failAfter    time.Duration
}

func NewPauseControl() *PauseControl {
	return &PauseControl{}
}

func (p *PauseControl) Pause(failAfter time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.paused {
		return ErrorAlreadyPaused
	}

	p.paused = true
	p.pauseChannel = make(chan bool)
	p.failAfter = failAfter

	return nil
}

func (p *PauseControl) Resume() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if !p.paused {
		return ErrorNotPaused
	}

	p.paused = false
	close(p.pauseChannel)

	return nil
}

func (p *PauseControl) Wait() bool {
	free, pauseChannel, failChannel := p.getWaitState()
	if free {
		return true
	}

	select {
	case <-pauseChannel:
		return true
	case <-failChannel:
		return false
	}
}

func (p *PauseControl) getWaitState() (bool, chan bool, <-chan time.Time) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if !p.paused {
		return true, nil, nil
	}

	return false, p.pauseChannel, time.After(p.failAfter)
}
