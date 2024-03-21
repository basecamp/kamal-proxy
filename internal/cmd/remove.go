package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/mproxy/internal/server"
)

type removeCommand struct {
	cmd  *cobra.Command
	host string
}

func newRemoveCommand() *removeCommand {
	removeCommand := &removeCommand{}
	removeCommand.cmd = &cobra.Command{
		Use:   "remove",
		Short: "Remove the service for a host",
		RunE:  removeCommand.run,
		Args:  cobra.NoArgs,
	}

	removeCommand.cmd.Flags().StringVar(&removeCommand.host, "host", "", "Host to remote (empty for wildcard)")

	return removeCommand
}

func (c *removeCommand) run(cmd *cobra.Command, args []string) error {
	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		args := server.RemoveArgs{
			Host: c.host,
		}

		err := client.Call("mproxy.Remove", args, &response)

		return err
	})
}
