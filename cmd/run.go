package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/pkg/server"
)

var runOptions struct {
	port int
}

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the proxy server",
	RunE:  runServer,
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().IntVarP(&runOptions.port, "port", "p", 80, "Port to serve HTTP traffic on")
}

func runServer(cmd *cobra.Command, args []string) error {
	c := server.Config{
		ListenAddress: fmt.Sprintf(":%d", runOptions.port),
		SocketPath:    globalOptions.socketPath,
	}
	s := server.NewServer(c)

	err := s.Start()
	if err != nil {
		return err
	}
	defer s.Stop()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch

	return nil
}
