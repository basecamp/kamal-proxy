package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var ErrorNoHealthyTargets = errors.New("no healthy targets")

type TargetList []*Target

func NewTargetList(targetNames []string, options TargetOptions) (TargetList, error) {
	targets := TargetList{}

	for _, name := range targetNames {
		target, err := NewTarget(name, options)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}

	return targets, nil
}

func (tl TargetList) Names() []string {
	names := []string{}
	for _, target := range tl {
		names = append(names, target.Target())
	}
	return names
}

func (tl TargetList) Dispose() {
	for _, target := range tl {
		target.Dispose()
	}
}

type LoadBalancer struct {
	healthy TargetList
	all     TargetList
	index   int
	lock    sync.Mutex
}

func NewLoadBalancer(targets TargetList) *LoadBalancer {
	lb := &LoadBalancer{
		healthy: TargetList{},
		all:     targets,
	}

	lb.beginHealthChecks()
	return lb
}

func (lb *LoadBalancer) Targets() TargetList {
	lb.lock.Lock()
	defer lb.lock.Unlock()

	return lb.all
}

func (lb *LoadBalancer) WaitUntilHealthy(timeout time.Duration) error {
	var wg sync.WaitGroup
	var failed atomic.Bool

	wg.Add(len(lb.Targets()))

	for _, target := range lb.Targets() {
		go func() {
			if !target.WaitUntilHealthy(timeout) {
				slog.Info("Target failed to become healthy", "target", target.Target())
				failed.Store(true)
			}
			wg.Done()
		}()
	}

	wg.Wait()

	if failed.Load() {
		return fmt.Errorf("%w (%s)", ErrorTargetFailedToBecomeHealthy, timeout)
	}

	return nil
}

func (lb *LoadBalancer) MarkAllHealthy() {
	for _, target := range lb.Targets() {
		target.updateState(TargetStateHealthy)
	}
	lb.updateHealthyTargets()
}

func (lb *LoadBalancer) Dispose() {
	lb.lock.Lock()
	defer lb.lock.Unlock()

	lb.all.Dispose()
}

func (lb *LoadBalancer) DrainAll(timeout time.Duration) {
	var wg sync.WaitGroup
	wg.Add(len(lb.all))

	for _, target := range lb.all {
		go func() {
			target.Drain(timeout)
			wg.Done()
		}()
	}

	wg.Wait()
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, req, err := lb.claimTarget(r)
	if err != nil {
		SetErrorResponse(w, r, http.StatusServiceUnavailable, nil)
		return
	}

	target.SendRequest(w, req)
}

// TargetStateConsumer

func (lb *LoadBalancer) TargetStateChanged(target *Target) {
	lb.updateHealthyTargets()
}

// Private

func (lb *LoadBalancer) claimTarget(req *http.Request) (*Target, *http.Request, error) {
	lb.lock.Lock()
	defer lb.lock.Unlock()

	target := lb.nextTarget()
	if target == nil {
		return nil, nil, ErrorNoHealthyTargets
	}

	req, err := target.StartRequest(req)
	return target, req, err
}

func (lb *LoadBalancer) nextTarget() *Target {
	if len(lb.healthy) == 0 {
		return nil
	}

	lb.index = (lb.index + 1) % len(lb.healthy)
	return lb.healthy[lb.index]
}

func (lb *LoadBalancer) beginHealthChecks() {
	for _, target := range lb.all {
		target.BeginHealthChecks(lb)
	}
}

func (lb *LoadBalancer) updateHealthyTargets() {
	lb.lock.Lock()
	defer lb.lock.Unlock()

	lb.healthy = TargetList{}
	for _, target := range lb.all {
		if target.State() == TargetStateHealthy {
			lb.healthy = append(lb.healthy, target)
		}
	}
}
