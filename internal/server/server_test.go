package server

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Deploying(t *testing.T) {
	_, target := testBackend(t, "first", http.StatusOK)
	server, addr := testServer(t)

	testDeployTarget(t, target, server)

	resp, err := http.Get(addr)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServer_DeployingGaplessly(t *testing.T) {
	_, initialTarget := testBackend(t, "first", http.StatusOK)

	newTargets := []*Target{}
	for i := 0; i < 5; i++ {
		_, target := testBackend(t, fmt.Sprintf("replacement %d", i), http.StatusOK)
		newTargets = append(newTargets, target)
	}

	server, addr := testServer(t)

	testDeployTarget(t, initialTarget, server)

	cc := newClientConsumer(addr)

	for _, target := range newTargets {
		time.Sleep(time.Millisecond * 10)
		testDeployTarget(t, target, server)
	}

	cc.Stop()

	assert.NotZero(t, cc.resultCount)
	assert.Zero(t, cc.statusCodes[http.StatusServiceUnavailable])
	assert.Zero(t, cc.statusCodes[http.StatusBadGateway])

	assert.Equal(t, cc.resultCount, cc.statusCodes[http.StatusOK])
}

// Helpers

func testDeployTarget(t *testing.T, target *Target, server *Server) {
	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		HealthCheckConfig: defaultHealthCheckConfig,
		TargetURL:         target.Target(),
		DeployTimeout:     DefaultDeployTimeout,
		DrainTimeout:      DefaultDrainTimeout,
	}, &result)

	require.NoError(t, err)
}

type clientConsumer struct {
	addr    string
	workers int
	wg      sync.WaitGroup
	done    chan struct{}

	resultCount int
	statusCodes map[int]int
	resultLock  sync.Mutex
}

func newClientConsumer(addr string) *clientConsumer {
	cc := &clientConsumer{
		addr:        addr,
		workers:     24,
		done:        make(chan struct{}),
		statusCodes: make(map[int]int),
	}

	cc.Start()
	return cc
}

func (cc *clientConsumer) Start() {
	cc.wg.Add(cc.workers)

	for i := 0; i < cc.workers; i++ {
		go cc.worker()
	}
}

func (cc *clientConsumer) Stop() {
	close(cc.done)
	cc.wg.Wait()
}

func (cc *clientConsumer) worker() {
	for {
		select {
		case <-cc.done:
			cc.wg.Done()
			return
		default:
			cc.sendRequest()
		}
	}
}

func (cc *clientConsumer) sendRequest() {
	resp, err := http.Get(cc.addr)

	statusCode := 0
	if err == nil {
		statusCode = resp.StatusCode
	}

	cc.resultLock.Lock()
	cc.resultCount++
	cc.statusCodes[statusCode]++
	cc.resultLock.Unlock()
}
