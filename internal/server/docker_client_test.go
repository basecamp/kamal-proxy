package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerClientNegotiatesNewDaemonAndUsesStartStopPaths(t *testing.T) {
	var pathsMu sync.Mutex
	var paths []string
	client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) {
		pathsMu.Lock()
		paths = append(paths, r.URL.Path)
		pathsMu.Unlock()
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"ApiVersion":"1.52","MinAPIVersion":"1.44"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, client.StopContainer(context.Background(), "test-container"))
	require.NoError(t, client.StartContainer(context.Background(), "test-container"))
	assert.Equal(t, []string{
		"/version",
		"/v1.52/containers/test-container/stop",
		"/v1.52/containers/test-container/start",
	}, paths)
}

func TestDockerClientNegotiatesOldCompatibleDaemon(t *testing.T) {
	client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"ApiVersion":"1.41","MinAPIVersion":"1.24"}`)
			return
		}
		assert.Equal(t, "/v1.41/containers/web/stop", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, client.StopContainer(context.Background(), "web"))
}

func TestDockerClientFallsBackWhenVersionEndpointFails(t *testing.T) {
	client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			http.Error(w, "not exposed", http.StatusNotFound)
			return
		}
		assert.Equal(t, "/v1.41/containers/web/start", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, client.StartContainer(context.Background(), "web"))
}

func TestDockerClientRejectsMalformedVersionResponse(t *testing.T) {
	for name, body := range map[string]string{
		"malformed json":  `{`,
		"invalid version": `{"ApiVersion":"new","MinAPIVersion":"1.44"}`,
		"invalid range":   `{"ApiVersion":"1.41","MinAPIVersion":"1.44"}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			err := client.StartContainer(context.Background(), "web")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "docker")
		})
	}
}

func TestDockerClientNegotiatesOnlyOnceWithConcurrentFirstUse(t *testing.T) {
	var versionCalls atomic.Int32
	client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			versionCalls.Add(1)
			fmt.Fprint(w, `{"ApiVersion":"1.52","MinAPIVersion":"1.44"}`)
			return
		}
		assert.True(t, strings.HasPrefix(r.URL.Path, "/v1.52/containers/"))
		w.WriteHeader(http.StatusNoContent)
	})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, client.StartContainer(context.Background(), "web"))
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), versionCalls.Load())
}

func TestDockerClientIncludesBoundedDockerErrorBody(t *testing.T) {
	client := testDockerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"ApiVersion":"1.52","MinAPIVersion":"1.44"}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "minimum API 1.44: "+strings.Repeat("x", maxDockerErrorBody*2))
	})
	err := client.StopContainer(context.Background(), "web")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minimum API 1.44")
	assert.LessOrEqual(t, len(err.Error()), maxDockerErrorBody+100)
	assert.True(t, strings.HasSuffix(err.Error(), "…"))
}

func testDockerClient(t *testing.T, handler http.HandlerFunc) *DockerClient {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	socketPath := t.TempDir() + "/docker.sock"
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return NewDockerClient(socketPath)
}
