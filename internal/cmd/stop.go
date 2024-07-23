package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type stopCommand struct {
	cmd  *cobra.Command
	args server.StopArgs
}

func newStopCommand() *stopCommand {
	stopCommand := &stopCommand{}
	stopCommand.cmd = &cobra.Command{
		Use:       "stop <service>",
		Short:     "Stop a service",
		RunE:      stopCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	stopCommand.cmd.Flags().DurationVar(&stopCommand.args.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "How long to allow in-flight requests to complete")

	return stopCommand
}

func (c *stopCommand) run(cmd *cobra.Command, args []string) error {
	var response bool

	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		return client.Call("kamal-proxy.Stop", c.args, &response)
	})
}
