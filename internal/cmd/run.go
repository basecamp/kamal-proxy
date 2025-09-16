package cmd

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type runCommand struct {
	cmd              *cobra.Command
	debugLogsEnabled bool
}

func newRunCommand() *runCommand {
	runCommand := &runCommand{}
	runCommand.cmd = &cobra.Command{
		Use:   "run",
		Short: "Run the server",
		RunE:  runCommand.run,
	}

	runCommand.cmd.Flags().BoolVar(&runCommand.debugLogsEnabled, "debug", getEnvBool("DEBUG", false), "Include debugging logs")
	runCommand.cmd.Flags().IntVar(&globalConfig.HttpPort, "http-port", getEnvInt("HTTP_PORT", server.DefaultHttpPort), "Port to serve HTTP traffic on")
	runCommand.cmd.Flags().IntVar(&globalConfig.HttpsPort, "https-port", getEnvInt("HTTPS_PORT", server.DefaultHttpsPort), "Port to serve HTTPS traffic on")
	runCommand.cmd.Flags().IntVar(&globalConfig.MetricsPort, "metrics-port", getEnvInt("METRICS_PORT", 0), "Publish metrics on the specified port (default zero to disable)")
	runCommand.cmd.Flags().BoolVar(&globalConfig.HTTP3Enabled, "http3", false, "Enable HTTP/3")

	return runCommand
}

func (c *runCommand) run(cmd *cobra.Command, args []string) error {
	c.setLogger()

	router := server.NewRouter(globalConfig.StatePath())
	router.RestoreLastSavedState()

	s := server.NewServer(&globalConfig, router)
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

func (c *runCommand) setLogger() {
	level := slog.LevelInfo
	if c.debugLogsEnabled {
		level = slog.LevelDebug
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
