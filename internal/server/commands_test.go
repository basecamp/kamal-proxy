package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandHandler_DeployAsyncDrain(t *testing.T) {
	router := testRouter(t)
	handler := NewCommandHandler(router)
	streamClosed := make(chan struct{})
	requestStarted := make(chan struct{})
	requestFinished := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-streamClosed:
			case <-r.Context().Done():
			}
		case "/slow":
			close(requestStarted)
			select {
			case <-requestFinished:
				io.WriteString(w, "finished")
			case <-r.Context().Done():
			}
		}
	}))
	t.Cleanup(backend.Close)
	oldTarget := strings.TrimPrefix(backend.URL, "http://")
	require.NoError(t, router.DeployService("service", []string{oldTarget}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))
	proxy := httptest.NewServer(router)
	t.Cleanup(proxy.Close)
	t.Cleanup(func() {
		select {
		case <-streamClosed:
		default:
			close(streamClosed)
		}
		select {
		case <-requestFinished:
		default:
			close(requestFinished)
		}
	})

	client := &http.Client{Timeout: 5 * time.Second}
	stream, err := client.Get(proxy.URL + "/events")
	require.NoError(t, err)
	t.Cleanup(func() { stream.Body.Close() })
	response := make(chan string, 1)
	go func() {
		res, err := client.Get(proxy.URL + "/slow")
		if err != nil {
			response <- err.Error()
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		response <- string(body)
	}()
	select {
	case <-requestStarted:
	case body := <-response:
		t.Fatalf("ordinary request did not reach the old target: %s", body)
	case <-time.After(time.Second):
		t.Fatal("ordinary request did not start")
	}

	_, newTarget := testBackend(t, "replacement", http.StatusOK)
	var drainID string
	require.NoError(t, handler.DeployAsyncDrain(DeployArgs{
		Service: "service", TargetURLs: []string{newTarget}, ServiceOptions: defaultServiceOptions,
		TargetOptions: defaultTargetOptions, DeploymentOptions: DeploymentOptions{DeployTimeout: time.Second, DrainTimeout: 5 * time.Second},
	}, &drainID))
	require.NotEmpty(t, drainID)
	status, body := sendGETRequest(router, "http://example.com/")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "replacement", body)
	var secondDrainID string
	require.NoError(t, handler.DeployAsyncDrain(DeployArgs{
		Service: "service", TargetURLs: []string{newTarget}, ServiceOptions: defaultServiceOptions,
		TargetOptions: defaultTargetOptions, DeploymentOptions: defaultDeploymentOptions,
	}, &secondDrainID))
	require.NotEqual(t, drainID, secondDrainID)
	var result bool
	require.NoError(t, handler.WaitDrain(secondDrainID, &result))

	waited := make(chan error, 1)
	go func() {
		var result bool
		waited <- handler.WaitDrain(drainID, &result)
	}()
	close(streamClosed)
	_, err = io.ReadAll(stream.Body)
	require.NoError(t, err)
	select {
	case err := <-waited:
		t.Fatalf("drain returned before the ordinary request finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(requestFinished)
	require.Equal(t, "finished", <-response)
	select {
	case err := <-waited:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after the last request completed")
	}

	require.ErrorContains(t, handler.WaitDrain(drainID, &result), "unknown drain")
}

func TestCommandHandler_DeployAsyncDrainUnhealthyReplacement(t *testing.T) {
	router := testRouter(t)
	handler := NewCommandHandler(router)
	_, oldTarget := testBackend(t, "original", http.StatusOK)
	_, newTarget := testBackend(t, "unhealthy", http.StatusServiceUnavailable)
	require.NoError(t, router.DeployService("service", []string{oldTarget}, defaultEmptyReaders, defaultServiceOptions, defaultTargetOptions, defaultDeploymentOptions))

	var drainID string
	err := handler.DeployAsyncDrain(DeployArgs{
		Service: "service", TargetURLs: []string{newTarget}, ServiceOptions: defaultServiceOptions,
		TargetOptions: defaultTargetOptions, DeploymentOptions: DeploymentOptions{DeployTimeout: 20 * time.Millisecond},
	}, &drainID)
	require.ErrorIs(t, err, ErrorTargetFailedToBecomeHealthy)
	require.Empty(t, drainID)
	_, body := sendGETRequest(router, "http://example.com/")
	require.Equal(t, "original", body)
}

func TestCommandHandler_WaitDrainUnknown(t *testing.T) {
	var result bool
	err := NewCommandHandler(testRouter(t)).WaitDrain("unknown", &result)
	require.ErrorContains(t, err, "unknown drain")
}

func TestCommandHandler_DeployRejectsInvalidServiceOptions(t *testing.T) {
	handler := NewCommandHandler(testRouter(t))

	var result bool
	err := handler.Deploy(DeployArgs{
		ServiceOptions: ServiceOptions{TLSEnabled: true},
	}, &result)

	require.ErrorContains(t, err, "host must be set when using TLS")
	require.ErrorIs(t, err, ErrServiceOptionsInvalid)
}
