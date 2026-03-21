package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerClient_StopStart(t *testing.T) {
	stopCalled := false
	startCalled := false

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.41/containers/test-container/stop" {
			stopCalled = true
			w.WriteHeader(http.StatusNoContent)
		} else if r.URL.Path == "/v1.41/containers/test-container/start" {
			startCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))

	socketPath := t.TempDir() + "/docker.sock"
	l, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	server.Listener = l
	server.Start()
	defer server.Close()

	client := NewDockerClient(socketPath)

	err = client.StopContainer(context.Background(), "test-container")
	assert.NoError(t, err)
	assert.True(t, stopCalled)

	err = client.StartContainer(context.Background(), "test-container")
	assert.NoError(t, err)
	assert.True(t, startCalled)
}
