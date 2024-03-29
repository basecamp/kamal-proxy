package cmd

import (
	"encoding/json"
	"net/rpc"
	"os"

	"github.com/spf13/cobra"

	"github.com/basecamp/parachute/internal/server"
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
	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response server.ListResponse

		err := client.Call("parachute.List", true, &response)
		if err != nil {
			return err
		}

		json.NewEncoder(os.Stdout).Encode(response)

		return nil
	})
}
