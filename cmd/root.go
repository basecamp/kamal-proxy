package cmd

import (
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/pkg/server"
)

var serverConfig server.Config

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:          "mproxy",
	Short:        "Minimal HTTP proxy for zero downtime deployments",
	Long:         `TODO`,
	SilenceUsage: true,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serverConfig.ConfigDir, "config", defaultConfigLocation(), "Path to config location")
	rootCmd.PersistentFlags().BoolVar(&serverConfig.Debug, "debug", false, "Include debugging logs")
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
