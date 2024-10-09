package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Deploying(t *testing.T) {
	t.Parallel()

	target := testTarget(t, func(w http.ResponseWriter, r *http.Request) {})
	server, addr := testServer(t)

	testDeployTarget(t, target, server)

	resp, err := http.Get(addr)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// Helpers

func testDeployTarget(t *testing.T, target *Target, server *Server) {
	var result bool
	err := server.commandHandler.Deploy(DeployArgs{
		TargetURL:      target.Target(),
		DeployTimeout:  DefaultDeployTimeout,
		DrainTimeout:   DefaultDrainTimeout,
		ServiceOptions: defaultServiceOptions,
		TargetOptions:  defaultTargetOptions,
	}, &result)

	require.NoError(t, err)
}
