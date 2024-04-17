package server

import (
	"errors"
	"sync"
)

var (
	ErrorAlreadyPaused = errors.New("already paused")
	ErrorNotPaused     = errors.New("not paused")
)

type PauseControl struct {
	lock  sync.RWMutex
	guard chan bool
}

func NewPauseControl() *PauseControl {
	return &PauseControl{
		guard: make(chan bool, 1),
	}
}

func (p *PauseControl) Pause() error {
	select {
	case p.guard <- true:
	default:
		return ErrorAlreadyPaused
	}

	p.lock.Lock()
	return nil
}

func (p *PauseControl) Resume() error {
	select {
	case <-p.guard:
	default:
		return ErrorNotPaused
	}

	p.lock.Unlock()
	return nil
}

func (p *PauseControl) Wait() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return true
}
