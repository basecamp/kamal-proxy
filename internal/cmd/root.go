package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

var globalConfig server.Config

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:          "kamal-proxy",
	Short:        "HTTP proxy for zero downtime deployments",
	SilenceUsage: true,
}

func Execute() {
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.PersistentFlags().StringVar(&globalConfig.ConfigDir, "state-path", "", "Path to store state; empty to use default system paths")

	rootCmd.AddCommand(newRunCommand().cmd)
	rootCmd.AddCommand(newDeployCommand().cmd)
	rootCmd.AddCommand(newRemoveCommand().cmd)
	rootCmd.AddCommand(newPauseCommand().cmd)
	rootCmd.AddCommand(newStopCommand().cmd)
	rootCmd.AddCommand(newResumeCommand().cmd)
	rootCmd.AddCommand(newListCommand().cmd)
	rootCmd.AddCommand(newRolloutCommand().cmd)

	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
