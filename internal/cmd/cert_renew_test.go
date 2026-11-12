package cmd

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/kamal-proxy/internal/server"
)

func TestCertRenewCommand_ParsesHosts(t *testing.T) {
	cmd := newCertRenewCommand()
	require.NoError(t, cmd.cmd.ParseFlags([]string{"--host", "a.example.com", "--host", "b.example.com,c.example.com"}))

	assert.Equal(t, []string{"a.example.com", "b.example.com", "c.example.com"}, cmd.args.Hosts)
}

func TestCertRenewCommand_CallsServerOverRPC(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kamal-proxy.sock")
	t.Setenv("KAMAL_PROXY_SOCKET", socketPath)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(backend.Close)

	router := server.NewRouter(filepath.Join(dir, "state.json"))
	require.NoError(t, router.DeployService("plain", []string{backend.Listener.Addr().String()}, nil,
		server.ServiceOptions{Hosts: []string{"example.com"}},
		server.TargetOptions{HealthCheckConfig: server.HealthCheckConfig{Path: server.DefaultHealthCheckPath, Interval: server.DefaultHealthCheckInterval, Timeout: server.DefaultHealthCheckTimeout}, ResponseTimeout: server.DefaultTargetTimeout},
		server.DeploymentOptions{DeployTimeout: server.DefaultDeployTimeout, Force: true},
	))

	handler := server.NewCommandHandler(router)
	require.NoError(t, handler.Start(socketPath))
	t.Cleanup(func() { handler.Close() })

	run := func(args ...string) error {
		cmd := newCertRenewCommand()
		cmd.cmd.SetArgs(args)
		cmd.cmd.SilenceUsage = true
		cmd.cmd.SilenceErrors = true
		return cmd.cmd.Execute()
	}

	require.ErrorContains(t, run("missing"), server.ErrorServiceNotFound.Error())
	require.ErrorContains(t, run("plain"), server.ErrorCertRenewalTLSNotEnabled.Error())
	require.ErrorContains(t, run("plain", "--host", "example.com"), server.ErrorCertRenewalTLSNotEnabled.Error())
}
