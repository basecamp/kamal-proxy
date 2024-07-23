package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseControl_RunningByDefault(t *testing.T) {
	p := NewPauseControl()

	assert.Equal(t, PauseStateRunning, p.State())
	assert.Equal(t, PauseWaitActionProceed, p.Wait())
}

func TestPauseControl_WaitBlocksWhenPaused(t *testing.T) {
	p := NewPauseControl()
	var wg sync.WaitGroup

	require.NoError(t, p.Pause(time.Second))
	assert.Equal(t, PauseStatePaused, p.State())

	wg.Add(1)
	go func() {
		require.NoError(t, p.Resume())
		wg.Done()
	}()

	assert.Equal(t, PauseWaitActionProceed, p.Wait())
	wg.Wait()
}

func TestPauseControl_PausedWaitsCanTimeout(t *testing.T) {
	p := NewPauseControl()

	require.NoError(t, p.Pause(time.Millisecond))
	assert.Equal(t, PauseStatePaused, p.State())
	assert.Equal(t, PauseWaitActionTimedOut, p.Wait())
}

func TestPauseControl_Stopped(t *testing.T) {
	p := NewPauseControl()

	require.NoError(t, p.Stop())
	assert.Equal(t, PauseStateStopped, p.State())
	assert.Equal(t, PauseWaitActionUnavailable, p.Wait())
}

func TestPauseControl_StoppingPausedRequestsFailsThemImmediately(t *testing.T) {
	p := NewPauseControl()
	var wg sync.WaitGroup

	require.NoError(t, p.Pause(time.Second))
	assert.Equal(t, PauseStatePaused, p.State())

	wg.Add(1)
	go func() {
		require.NoError(t, p.Stop())
		wg.Done()
	}()

	assert.Equal(t, PauseWaitActionUnavailable, p.Wait())
	wg.Wait()
}
