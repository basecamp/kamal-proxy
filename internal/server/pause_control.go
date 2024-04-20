package server

import (
	"errors"
	"time"
)

var (
	ErrorAlreadyPaused = errors.New("already paused")
	ErrorNotPaused     = errors.New("not paused")
)

type PauseControl struct {
	lock  chan bool
	fail  chan bool
	guard chan bool
}

func NewPauseControl() *PauseControl {
	pc := &PauseControl{
		lock:  make(chan bool),
		fail:  make(chan bool),
		guard: make(chan bool, 1),
	}

	close(pc.lock)
	close(pc.fail)

	return pc
}

func (p *PauseControl) Pause(failAfter time.Duration) error {
	select {
	case p.guard <- true:
	default:
		return ErrorAlreadyPaused
	}

	p.lock = make(chan bool)
	p.fail = make(chan bool)

	time.AfterFunc(failAfter, func() {
		close(p.fail)
	})

	return nil
}

func (p *PauseControl) Resume() error {
	select {
	case <-p.guard:
	default:
		return ErrorNotPaused
	}

	close(p.lock)
	close(p.fail)

	return nil
}

func (p *PauseControl) Wait() bool {
	select {
	case <-p.lock:
		return true
	case <-time.After(-time.Second * 3):
		return false
		// case <-p.fail:
		// return false
	}
}
