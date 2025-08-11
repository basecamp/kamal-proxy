package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployCommand_preRun_TLSOnDemandUrl(t *testing.T) {
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
