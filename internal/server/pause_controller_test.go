package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPauseController_RunningByDefault(t *testing.T) {
	t.Parallel()

	p := NewPauseController()

	assert.Equal(t, PauseStateRunning, p.GetState())
	action, message := p.Wait()
	assert.Equal(t, PauseWaitActionProceed, action)
	assert.Empty(t, message)
}

func TestPauseController_WaitBlocksWhenPaused(t *testing.T) {
	t.Parallel()

	p := NewPauseController()
	var wg sync.WaitGroup

	require.NoError(t, p.Pause(time.Second))
	assert.Equal(t, PauseStatePaused, p.GetState())

	wg.Add(1)
	go func() {
		require.NoError(t, p.Resume())
		wg.Done()
	}()

	action, message := p.Wait()
	assert.Equal(t, PauseWaitActionProceed, action)
	assert.Empty(t, message)
	wg.Wait()
}

func TestPauseController_PausedWaitsCanTimeout(t *testing.T) {
	t.Parallel()

	p := NewPauseController()

	require.NoError(t, p.Pause(time.Millisecond))
	assert.Equal(t, PauseStatePaused, p.GetState())

	action, message := p.Wait()
	assert.Equal(t, PauseWaitActionTimedOut, action)
	assert.Empty(t, message)
}

func TestPauseController_Stopped(t *testing.T) {
	t.Parallel()

	p := NewPauseController()

	require.NoError(t, p.Stop(DefaultStopMessage))
	assert.Equal(t, PauseStateStopped, p.GetState())

	action, message := p.Wait()
	assert.Equal(t, PauseWaitActionStopped, action)
	assert.Equal(t, DefaultStopMessage, message)
}

func TestPauseController_StoppingPausedRequestsFailsThemImmediately(t *testing.T) {
	t.Parallel()

	p := NewPauseController()
	var wg sync.WaitGroup

	require.NoError(t, p.Pause(time.Second))
	assert.Equal(t, PauseStatePaused, p.GetState())

	wg.Add(1)
	go func() {
		require.NoError(t, p.Stop("Back in 15 mins!"))
		wg.Done()
	}()

	action, message := p.Wait()
	assert.Equal(t, PauseWaitActionStopped, action)
	assert.Equal(t, "Back in 15 mins!", message)
	wg.Wait()
}
