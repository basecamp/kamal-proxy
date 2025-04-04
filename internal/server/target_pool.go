package server

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrorNoHealthyTargets = errors.New("no healthy targets available")
)

type TargetPool struct {
	targets     []*Target
	current     atomic.Int64
	targetLock  sync.RWMutex
	targetCount atomic.Int64
}

func NewTargetPool() *TargetPool {
	return &TargetPool{
		targets: make([]*Target, 0),
		current: atomic.Int64{},
	}
}

func (tp *TargetPool) AddTarget(target *Target) {
	tp.targetLock.Lock()
	defer tp.targetLock.Unlock()

	tp.targets = append(tp.targets, target)
	tp.targetCount.Store(int64(len(tp.targets)))
}

func (tp *TargetPool) RemoveTarget(target *Target) {
	tp.targetLock.Lock()
	defer tp.targetLock.Unlock()

	for i, t := range tp.targets {
		if t == target {
		
			tp.targets = append(tp.targets[:i], tp.targets[i+1:]...)
			tp.targetCount.Store(int64(len(tp.targets)))
			return
		}
	}
}

func (tp *TargetPool) ReplaceTargets(targets []*Target) []*Target {
	tp.targetLock.Lock()
	defer tp.targetLock.Unlock()

	oldTargets := tp.targets
	tp.targets = targets
	tp.targetCount.Store(int64(len(targets)))

	return oldTargets
}

func (tp *TargetPool) NextTarget() (*Target, error) {
	tp.targetLock.RLock()
	defer tp.targetLock.RUnlock()

	if len(tp.targets) == 0 {
		return nil, ErrorNoHealthyTargets
	}

	count := tp.targetCount.Load()
	if count == 0 {
		return nil, ErrorNoHealthyTargets
	}



	current := tp.current.Load()
	next := (current + 1) % count
	tp.current.Store(next)

	return tp.targets[next], nil
}

func (tp *TargetPool) GetTargets() []*Target {
	tp.targetLock.RLock()
	defer tp.targetLock.RUnlock()


	targets := make([]*Target, len(tp.targets))
	copy(targets, tp.targets)
	return targets
}

func (tp *TargetPool) StartRequest(req *http.Request) (*Target, *http.Request, error) {

	tp.targetLock.RLock()
	targetCount := len(tp.targets)
	tp.targetLock.RUnlock()

	if targetCount == 0 {
		return nil, nil, ErrorNoHealthyTargets
	}


	for i := 0; i < targetCount; i++ {
		target, err := tp.NextTarget()
		if err != nil {
			return nil, nil, err
		}

		newReq, err := target.StartRequest(req)
		if err == nil {
			return target, newReq, nil
		}

	
		if errors.Is(err, ErrorDraining) {
			slog.Debug("Target is draining, trying next target", "target", target.Target())
			continue
		}

	
		return nil, nil, err
	}


	return nil, nil, ErrorNoHealthyTargets
}

func (tp *TargetPool) Drain(timeout time.Duration) {
	tp.targetLock.RLock()
	targets := make([]*Target, len(tp.targets))
	copy(targets, tp.targets)
	tp.targetLock.RUnlock()

	for _, target := range targets {
		target.Drain(timeout)
	}
}

func (tp *TargetPool) StopHealthChecks() {
	tp.targetLock.RLock()
	targets := make([]*Target, len(tp.targets))
	copy(targets, tp.targets)
	tp.targetLock.RUnlock()

	for _, target := range targets {
		target.StopHealthChecks()
	}
}

func (tp *TargetPool) Count() int {
	return int(tp.targetCount.Load())
}
