package cmd

import (
	"net/rpc"

	"github.com/basecamp/kamal-proxy/internal/server"
	"github.com/spf13/cobra"
)

type rolloutStopCommand struct {
	cmd  *cobra.Command
	args server.RolloutStopArgs
}

func newRolloutStopCommand() *rolloutStopCommand {
	rolloutStopCommand := &rolloutStopCommand{}
	rolloutStopCommand.cmd = &cobra.Command{
		Use:       "stop <service>",
		Short:     "Stops rollout of a service",
		RunE:      rolloutStopCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	rolloutStopCommand.cmd.Flags().DurationVar(&rolloutStopCommand.args.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "How long to allow in-flight requests to complete")

	return rolloutStopCommand
}

func (c *rolloutStopCommand) run(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.RolloutStop", c.args, &response)
	})
}
