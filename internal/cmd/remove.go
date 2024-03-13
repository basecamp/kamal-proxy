package cmd

import (
	"net/rpc"
	"path"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/internal/server"
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
		RunE:  removeCommand.removeService,
		Args:  cobra.NoArgs,
	}

	removeCommand.cmd.Flags().StringVar(&removeCommand.host, "host", "", "Host to remote (empty for wildcard)")

	return removeCommand
}

func (c *removeCommand) removeService(cmd *cobra.Command, args []string) error {
	socketPath := path.Join(configDir, "mproxy.sock") // TODO: move this somewhere shared

	return c.invoke(socketPath, c.host)
}

func (c *removeCommand) invoke(socketPath string, host string) error {
	return withRPCClient(socketPath, func(client *rpc.Client) error {
		var response bool
		args := server.RemoveArgs{
			Host: host,
		}

		err := client.Call("mproxy.Remove", args, &response)

		return err
	})
}
