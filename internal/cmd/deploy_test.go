package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/kamal-proxy/internal/server"
)

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
