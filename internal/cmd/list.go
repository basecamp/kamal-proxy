package cmd

import (
	"encoding/json"
	"net/rpc"
	"os"
	"path"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/internal/server"
)

type listCommand struct {
	cmd *cobra.Command
}

func newListCommand() *listCommand {
	listCommand := &listCommand{}
	listCommand.cmd = &cobra.Command{
		Use:   "list",
		Short: "List the services currently running",
		RunE:  listCommand.run,
		Args:  cobra.NoArgs,
	}

	return listCommand
}

func (c *listCommand) run(cmd *cobra.Command, args []string) error {
	socketPath := path.Join(configDir, "mproxy.sock") // TODO: move this somewhere shared

	return c.invoke(socketPath)
}

func (c *listCommand) invoke(socketPath string) error {
	return withRPCClient(socketPath, func(client *rpc.Client) error {
		var response server.ListResponse

		err := client.Call("mproxy.List", true, &response)
		if err != nil {
			return err
		}

		json.NewEncoder(os.Stdout).Encode(response)

		return nil
	})
}
