package cmd

import (
	"net/rpc"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/parachute/internal/server"
)

type pauseCommand struct {
	cmd          *cobra.Command
	host         string
	drainTimeout time.Duration
	pauseTimeout time.Duration
}

func newPauseCommand() *pauseCommand {
	pauseCommand := &pauseCommand{}
	pauseCommand.cmd = &cobra.Command{
		Use:   "pause",
		Short: "Pause a service",
		RunE:  pauseCommand.run,
		Args:  cobra.NoArgs,
	}

	pauseCommand.cmd.Flags().StringVar(&pauseCommand.host, "host", "", "Host to pause (empty for wildcard)")
	pauseCommand.cmd.Flags().DurationVar(&pauseCommand.drainTimeout, "drain-timeout", server.DefaultDrainTimeout, "How long to allow in-flight requests to complete")
	pauseCommand.cmd.Flags().DurationVar(&pauseCommand.pauseTimeout, "max-pause", server.DefaultPauseTimeout, "How long to enqueue requests before shedding load")

	return pauseCommand
}

func (c *pauseCommand) run(cmd *cobra.Command, args []string) error {
	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		args := server.PauseArgs{
			Host:         c.host,
			DrainTimeout: c.drainTimeout,
			PauseTimeout: c.pauseTimeout,
		}

		err := client.Call("parachute.Pause", args, &response)

		return err
	})
}
