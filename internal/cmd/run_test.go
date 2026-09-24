package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCommandReturnsStateRestoreError(t *testing.T) {
	previous := globalConfig
	t.Cleanup(func() { globalConfig = previous })
	globalConfig.AlternateConfigDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(globalConfig.AlternateConfigDir, "kamal-proxy.state"), []byte("invalid"), 0o600))

	err := newRunCommand().run(nil, nil)

	require.ErrorContains(t, err, "invalid character 'i'")
}
