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
		RunE:      rolloutStopCommand.stop,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	return rolloutStopCommand
}

func (c *rolloutStopCommand) stop(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.RolloutStop", c.args, &response)
	})
}
