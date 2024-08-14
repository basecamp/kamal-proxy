package cmd

import (
	"fmt"
	"net/rpc"
	"strings"

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
	fmt.Println(c.format("Service", 30, italic) +
		c.format("Host", 30, italic) + c.format("Target", 30, italic) +
		c.format("State", 10, italic) + c.format("TLS", 10, italic))

	for name, service := range reponse.Targets {
		tls := "no"
		if service.TLS {
			tls = "yes"
		}

		fmt.Println(c.format(name, 30, bold) +
			c.format(service.Host, 30, plain) + c.format(service.Target, 30, plain) +
			c.format(service.State, 10, plain) + c.format(tls, 10, plain))
	}
}

const (
	plain  = ""
	bold   = "1;34"
	italic = "3;94"
)

func (c *listCommand) format(text string, width int, style string) string {
	paddingLength := max(0, width-len(text))
	text = (text + strings.Repeat(" ", paddingLength))[0:width]
	if style != "" {
		text = "\033[" + style + "m" + text + "\033[0m"
	}

	return text
}
