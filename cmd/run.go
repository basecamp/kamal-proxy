package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/pkg/server"
)

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the proxy server",
	RunE:  runServer,
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().IntVarP(&serverConfig.ListenPort, "port", "p", 80, "Port to serve HTTP traffic on")
	runCmd.Flags().DurationVar(&serverConfig.AddTimeout, "add-timeout", server.DefaultAddTimeout, "Max time to wait for new services to become healthy before returning an error")
	runCmd.Flags().DurationVar(&serverConfig.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "Time to wait for service to drain before killing connections")
	runCmd.Flags().IntVar(&serverConfig.MaxRequestBodySize, "max-request-body", 0, "Max size of request body (0 means unlimited)")
	runCmd.Flags().StringVar(&serverConfig.HealthCheckPath, "health-path", server.DefaultHealthCheckPath, "Path for healthcheck")
	runCmd.Flags().DurationVar(&serverConfig.HealthCheckInterval, "health-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	runCmd.Flags().DurationVar(&serverConfig.HealthCheckTimeout, "health-timeout", server.DefaultHealthCheckTimeout, "Max time allowed for health check to respond")
}

func runServer(cmd *cobra.Command, args []string) error {
	s := server.NewServer(serverConfig)

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
