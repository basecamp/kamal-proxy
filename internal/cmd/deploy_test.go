package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestDeployCommand_preRun_TLSOnDemandURL(t *testing.T) {
	t.Run("TLS enabled with TLS on-demand URL should set hosts to empty string", func(t *testing.T) {
		deployCmd := newDeployCommand()

		// Set flags for TLS with on-demand URL
		deployCmd.cmd.Flags().Set("target", "http://localhost:8080")
		deployCmd.cmd.Flags().Set("tls", "true")
		deployCmd.cmd.Flags().Set("tls-on-demand-url", "http://example.com/validate")
		deployCmd.cmd.Flags().Set("host", "example.com")
		deployCmd.cmd.Flags().Set("path-prefix", "/")

		// Call preRun
		err := deployCmd.preRun(deployCmd.cmd, []string{"test-service"})
		require.NoError(t, err)

		// Verify that hosts is set to empty string
		assert.Equal(t, []string{""}, deployCmd.args.ServiceOptions.Hosts)
	})

	t.Run("TLS enabled without TLS on-demand URL should not modify hosts", func(t *testing.T) {
		deployCmd := newDeployCommand()

		// Set flags for TLS without on-demand URL
		deployCmd.cmd.Flags().Set("target", "http://localhost:8080")
		deployCmd.cmd.Flags().Set("tls", "true")
		deployCmd.cmd.Flags().Set("host", "example.com")
		deployCmd.cmd.Flags().Set("path-prefix", "/")

		// Call preRun
		err := deployCmd.preRun(deployCmd.cmd, []string{"test-service"})
		require.NoError(t, err)

		// Verify that hosts is not modified
		assert.Equal(t, []string{"example.com"}, deployCmd.args.ServiceOptions.Hosts)
	})
}
