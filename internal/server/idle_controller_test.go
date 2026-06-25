package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdleController_IdleAndWake(t *testing.T) {
	stopCalled := make(chan bool, 1)
	startCalled := make(chan bool, 1)

	dockerServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.41/containers/test-container/stop" {
			stopCalled <- true
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/v1.41/containers/test-container/start" {
			startCalled <- true
			w.WriteHeader(http.StatusNoContent)
		}
	}))

	socketPath := t.TempDir() + "/docker.sock"
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	dockerServer.Listener = l
	dockerServer.Start()
	defer dockerServer.Close()

	dockerClient := NewDockerClient(socketPath)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	target, _ := NewTarget(backend.URL[7:], defaultTargetOptions)
	tl := TargetList{target}
	lb := NewLoadBalancer(tl, 0, false)
	lb.MarkAllHealthy()

	ic := NewIdleController("test", 100*time.Millisecond, time.Second, []string{"test-container"}, dockerClient, lb)
	defer ic.Close()

	// Initial state: active
	assert.Equal(t, IdleStateActive, ic.GetState())

	// Wait for idle to trigger
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for container to be stopped")
	}

	assert.Equal(t, IdleStateSleeping, ic.GetState())

	// Wake up on request
	action := ic.WaitIfSleeping()
	assert.Equal(t, IdleWaitActionProceed, action)

	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for container to be started")
	}

	assert.Equal(t, IdleStateActive, ic.GetState())
}
