package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type listCommand struct {
	cmd *cobra.Command
}

func newListCommand() *listCommand {
	listCommand := &listCommand{}
	listCommand.cmd = &cobra.Command{
		Use:     "list",
		Short:   "List the services currently running",
		RunE:    listCommand.run,
		Args:    cobra.NoArgs,
		Aliases: []string{"ls"},
	}

	return listCommand
}

func (c *listCommand) run(cmd *cobra.Command, args []string) error {
	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response server.ListResponse

		err := client.Call("kamal-proxy.List", true, &response)
		if err != nil {
			return err
		}

		c.displayResponse(response)
		return nil
	})
}

func (c *listCommand) displayResponse(reponse server.ListResponse) {
	table := NewTable()
	table.AddRow([]string{"Service", "Host", "Target", "State", "TLS"})
	for name, service := range reponse.Targets {
		tls := "no"
		if service.TLS {
			tls = "yes"
		}

		table.AddRow([]string{name, service.Host, service.Target, service.State, tls})
	}

	table.Print()
}
