package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/parachute/internal/server"
)

type removeCommand struct {
	cmd  *cobra.Command
	args server.RemoveArgs
}

func newRemoveCommand() *removeCommand {
	removeCommand := &removeCommand{}
	removeCommand.cmd = &cobra.Command{
		Use:       "remove <service>",
		Short:     "Remove the service",
		RunE:      removeCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	return removeCommand
}

func (c *removeCommand) run(cmd *cobra.Command, args []string) error {
	var response bool

	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		return client.Call("parachute.Remove", c.args, &response)
	})
}
