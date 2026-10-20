package server

import (
	"encoding/json"
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
	PauseWaitActionStopped
)

type PauseController struct {
	State       PauseState    `json:"state"`
	StopMessage string        `json:"stop_message"`
	FailAfter   time.Duration `json:"fail_after"`

	lock         sync.RWMutex
	pauseChannel chan bool
}

func NewPauseController() *PauseController {
	return &PauseController{}
}

func (p *PauseController) UnmarshalJSON(data []byte) error {
	type alias PauseController // Avoid infinite recursion when we call Unmarshal
	err := json.Unmarshal(data, (*alias)(p))
	if err != nil {
		return err
	}

	switch p.State {
	case PauseStateRunning:
		p.Resume()
	case PauseStatePaused:
		p.Pause(p.FailAfter)
	case PauseStateStopped:
		p.Stop(p.StopMessage)
	}

	return nil
}

func (p *PauseController) GetState() PauseState {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.State
}

func (p *PauseController) GetStopMessage() string {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.StopMessage
}

func (p *PauseController) Stop(message string) error {
	p.setState(PauseStateStopped, message)
	return nil
}

func (p *PauseController) Pause(failAfter time.Duration) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.State != PauseStatePaused {
		p.pauseChannel = make(chan bool)
	}

	p.State = PauseStatePaused
	p.StopMessage = ""
	p.FailAfter = failAfter
	return nil
}

func (p *PauseController) Resume() error {
	p.setState(PauseStateRunning, "")
	return nil
}

func (p *PauseController) Wait() (PauseWaitAction, string) {
	state, stopMessage, pauseChannel, failTimer := p.getWaitState()

	if failTimer != nil {
		defer failTimer.Stop()
	}

	switch state {
	case PauseStateRunning:
		return PauseWaitActionProceed, ""

	case PauseStateStopped:
		return PauseWaitActionStopped, stopMessage

	default:
		select {
		case <-pauseChannel:
			switch p.GetState() {
			case PauseStateStopped:
				return PauseWaitActionStopped, p.GetStopMessage()
			default:
				return PauseWaitActionProceed, ""
			}
		case <-failTimer.C:
			return PauseWaitActionTimedOut, ""
		}
	}
}

func (p *PauseController) getWaitState() (PauseState, string, chan bool, *time.Timer) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.State == PauseStatePaused {
		return PauseStatePaused, "", p.pauseChannel, time.NewTimer(p.FailAfter)
	}

	return p.State, p.StopMessage, nil, nil
}

func (p *PauseController) setState(newState PauseState, message string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.State != newState && p.State == PauseStatePaused {
		close(p.pauseChannel)
	}

	p.StopMessage = message
	p.State = newState
}
