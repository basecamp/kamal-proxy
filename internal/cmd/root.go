package cmd

import (
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/internal/server"
)

var globalConfig server.Config

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:          "mproxy",
	Short:        "HTTP proxy for zero downtime deployments",
	SilenceUsage: true,
}

func Execute() {
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.PersistentFlags().StringVar(&globalConfig.ConfigDir, "state-path", defaultConfigLocation(), "Path to store state")

	rootCmd.AddCommand(newRunCommand().cmd)
	rootCmd.AddCommand(newDeployCommand().cmd)
	rootCmd.AddCommand(newRemoveCommand().cmd)
	rootCmd.AddCommand(newListCommand().cmd)

	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func defaultConfigLocation() string {
	home, err := os.UserConfigDir()
	if err != nil {
		home = os.TempDir()
	}

	folder := path.Join(home, "mproxy")
	err = os.MkdirAll(folder, syscall.S_IRUSR|syscall.S_IWUSR|syscall.S_IXUSR)
	if err != nil {
		folder = os.TempDir()
	}

	return folder
}
