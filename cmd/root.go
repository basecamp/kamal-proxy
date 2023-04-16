package cmd

import (
	"os"
	"path"
	"syscall"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:          "mproxy",
	Short:        "Minimal HTTP proxy for zero downtime deployments",
	Long:         `TODO`,
	SilenceUsage: true,
}

var globalOptions struct {
	socketPath string
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&globalOptions.socketPath, "socket", defaultSocketFilename(), "Path to socket file")
}

func defaultSocketFilename() string {
	home, err := os.UserConfigDir()
	if err != nil {
		home = os.TempDir()
	}

	folder := path.Join(home, "mproxy")
	err = os.MkdirAll(folder, syscall.S_IRUSR|syscall.S_IWUSR|syscall.S_IXUSR)
	if err != nil {
		folder = os.TempDir()
	}

	return path.Join(folder, "mproxy.sock")
}
