package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLifecycle struct {
	starts, stops     atomic.Int32
	startErr, stopErr error
}

type blockingLifecycle struct {
	starts   atomic.Int32
	started  chan struct{}
	finished chan struct{}
}

func (f *blockingLifecycle) StartContainer(ctx context.Context, _ string) error {
	f.starts.Add(1)
	close(f.started)
	<-ctx.Done()
	close(f.finished)
	return ctx.Err()
}

func (f *blockingLifecycle) StopContainer(context.Context, string) error { return nil }

func (f *fakeLifecycle) StartContainer(context.Context, string) error {
	f.starts.Add(1)
	return f.startErr
}
func (f *fakeLifecycle) StopContainer(context.Context, string) error {
	f.stops.Add(1)
	return f.stopErr
}

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	require.Eventually(t, check, time.Second, time.Millisecond)
}

func TestIdleControllerDoesNotStopInflightRequest(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	c := NewIdleController(10*time.Millisecond, time.Second, []string{"web"}, lifecycle, func(time.Duration) error { return nil })
	defer c.Close()
	require.NoError(t, c.BeginRequest(context.Background()))
	time.Sleep(30 * time.Millisecond)
	assert.Zero(t, lifecycle.stops.Load())
	c.EndRequest()
	waitFor(t, func() bool { return lifecycle.stops.Load() == 1 })
}

func TestIdleControllerCoalescesConcurrentWake(t *testing.T) {
	lifecycle := &fakeLifecycle{}
	ready := make(chan struct{})
	c := NewIdleController(time.Millisecond, time.Second, []string{"web"}, lifecycle, func(time.Duration) error { <-ready; return nil })
	defer c.Close()
	waitFor(t, func() bool { return c.StateValue() == IdleStateSleeping })
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); require.NoError(t, c.BeginRequest(context.Background())); c.EndRequest() }()
	}
	waitFor(t, func() bool { return lifecycle.starts.Load() == 1 })
	close(ready)
	wg.Wait()
	assert.Equal(t, int32(1), lifecycle.starts.Load())
}

func TestIdleControllerWakeFailureAndTimeout(t *testing.T) {
	lifecycle := &fakeLifecycle{startErr: errors.New("start failed")}
	c := NewIdleController(time.Millisecond, time.Second, []string{"web"}, lifecycle, func(time.Duration) error { return nil })
	defer c.Close()
	waitFor(t, func() bool { return c.StateValue() == IdleStateSleeping })
	assert.ErrorContains(t, c.BeginRequest(context.Background()), "start failed")
	c.EndRequest()

	blocking := make(chan struct{})
	c.Reset([]string{"web"}, func(time.Duration) error { <-blocking; return nil })
	c.mu.Lock()
	c.State, c.WakeTimeout = IdleStateSleeping, 10*time.Millisecond
	c.mu.Unlock()
	lifecycle.startErr = nil
	assert.ErrorIs(t, c.BeginRequest(context.Background()), ErrIdleWakeTimeout)
	c.EndRequest()
	close(blocking)
}

func TestIdleControllerRestoresSleepingAndWakes(t *testing.T) {
	original := &IdleController{State: IdleStateWaking, IdleTimeout: time.Minute, WakeTimeout: time.Second, ContainerNames: []string{"web"}}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	var restored IdleController
	require.NoError(t, json.Unmarshal(data, &restored))
	lifecycle := &fakeLifecycle{}
	restored.configure(time.Minute, time.Second, []string{"web"}, lifecycle, func(time.Duration) error { return nil })
	defer restored.Close()
	assert.Equal(t, IdleStateSleeping, restored.StateValue())
	require.NoError(t, restored.BeginRequest(context.Background()))
	restored.EndRequest()
	assert.Equal(t, int32(1), lifecycle.starts.Load())
}

func TestIdleControllerResetCancelsWake(t *testing.T) {
	lifecycle := &blockingLifecycle{started: make(chan struct{}), finished: make(chan struct{})}
	readyCalled := atomic.Bool{}
	c := NewIdleController(time.Hour, time.Second, []string{"old"}, lifecycle, func(time.Duration) error {
		readyCalled.Store(true)
		return nil
	})
	defer c.Close()
	c.mu.Lock()
	c.State = IdleStateSleeping
	c.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- c.BeginRequest(context.Background()) }()
	<-lifecycle.started
	c.Reset([]string{"new"}, func(time.Duration) error { return nil })

	require.NoError(t, <-done)
	require.Eventually(t, func() bool {
		select {
		case <-lifecycle.finished:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, time.Millisecond)
	assert.False(t, readyCalled.Load())
	assert.Equal(t, IdleStateActive, c.StateValue())
}
