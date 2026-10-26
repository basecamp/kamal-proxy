package cmd

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/kamal-proxy/internal/server"
)

func TestDeployCommand_AsyncDrain(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "proxy.sock")
	t.Setenv("KAMAL_PROXY_SOCKET", socketPath)
	handler := server.NewCommandHandler(server.NewRouter(filepath.Join(t.TempDir(), "state.json")))
	rpcServer := rpc.NewServer()
	require.NoError(t, rpcServer.RegisterName("kamal-proxy", handler))
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go rpcServer.ServeConn(conn)
		}
	}()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(backend.Close)
	deploy := newDeployCommand()
	var output bytes.Buffer
	deploy.cmd.SetOut(&output)
	deploy.cmd.SetArgs([]string{"service", "--target", strings.TrimPrefix(backend.URL, "http://"), "--async-drain"})
	require.NoError(t, deploy.cmd.Execute())
	drainID := strings.TrimSpace(output.String())
	require.NotEmpty(t, drainID)
	require.NotContains(t, drainID, "\n")

	wait := newWaitDrainCommand()
	wait.cmd.SetArgs([]string{drainID})
	require.NoError(t, wait.cmd.Execute())
}

func TestDeployCommand_TLSRequiresHost(t *testing.T) {
	assertTLSHostValidation := func(t *testing.T, hosts []string, allowed bool) {
		t.Helper()

		cmd := newDeployCommand()
		cmd.args.ServiceOptions.Hosts = hosts
		cmd.args.ServiceOptions.TLSEnabled = true

		err := cmd.preRun(cmd.cmd, []string{"test-service"})

		if allowed {
			require.NoError(t, err)
		} else {
			require.ErrorContains(t, err, "host must be set when using TLS")
			require.ErrorIs(t, err, server.ErrServiceOptionsInvalid)
		}
	}

	assertTLSHostValidation(t, nil, false)
	assertTLSHostValidation(t, []string{""}, false)
	assertTLSHostValidation(t, []string{"*.example.com", ""}, false)

	assertTLSHostValidation(t, []string{"example.com"}, true)
	assertTLSHostValidation(t, []string{"example.com", "*.example.com"}, true)
}

func TestDeployCommand_TLSOnDemandURL(t *testing.T) {
	t.Run("host is not required when a TLS on-demand URL is set", func(t *testing.T) {
		cmd := newDeployCommand()
		cmd.args.ServiceOptions.TLSEnabled = true
		cmd.args.ServiceOptions.TLSOnDemandURL = "https://example.com/allow-host"

		require.NoError(t, cmd.preRun(cmd.cmd, []string{"test-service"}))
	})

	t.Run("hosts cannot be combined with a TLS on-demand URL", func(t *testing.T) {
		cmd := newDeployCommand()
		cmd.args.ServiceOptions.TLSEnabled = true
		cmd.args.ServiceOptions.TLSOnDemandURL = "https://example.com/allow-host"
		cmd.args.ServiceOptions.Hosts = []string{"example.com"}

		err := cmd.preRun(cmd.cmd, []string{"test-service"})
		require.ErrorContains(t, err, "cannot set hosts when using a TLS on-demand URL")
		require.ErrorIs(t, err, server.ErrServiceOptionsInvalid)
	})

	t.Run("the TLS on-demand URL must be valid", func(t *testing.T) {
		cmd := newDeployCommand()
		cmd.args.ServiceOptions.TLSEnabled = true
		cmd.args.ServiceOptions.TLSOnDemandURL = "ftp://example.com/allow-host"

		err := cmd.preRun(cmd.cmd, []string{"test-service"})
		require.ErrorContains(t, err, "unsupported scheme")
		require.ErrorIs(t, err, server.ErrServiceOptionsInvalid)
	})
}

func TestDeployCommand_CanonicalHostValidation(t *testing.T) {
	tests := []struct {
		name          string
		hosts         []string
		canonicalHost string
		expectError   bool
		expectedError string
	}{
		{
			name:          "valid canonical host in hosts list",
			hosts:         []string{"example.com", "www.example.com"},
			canonicalHost: "example.com",
			expectError:   false,
		},
		{
			name:          "valid canonical host in hosts list with www",
			hosts:         []string{"example.com", "www.example.com"},
			canonicalHost: "www.example.com",
			expectError:   false,
		},
		{
			name:          "canonical host not in hosts list",
			hosts:         []string{"example.com", "www.example.com"},
			canonicalHost: "api.example.com",
			expectError:   true,
			expectedError: "canonical-host 'api.example.com' must be present in the hosts list: [example.com www.example.com]",
		},
		{
			name:          "canonical host empty with hosts",
			hosts:         []string{"example.com", "www.example.com"},
			canonicalHost: "",
			expectError:   false,
		},
		{
			name:          "canonical host with no hosts",
			hosts:         []string{},
			canonicalHost: "example.com",
			expectError:   false,
		},
		{
			name:          "both canonical host and hosts empty",
			hosts:         []string{},
			canonicalHost: "",
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newDeployCommand()

			cmd.args.ServiceOptions.Hosts = tt.hosts
			cmd.args.ServiceOptions.CanonicalHost = tt.canonicalHost
			cmd.args.ServiceOptions.TLSEnabled = false

			mockCmd := &cobra.Command{}

			err := cmd.preRun(mockCmd, []string{"test-service"})

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
