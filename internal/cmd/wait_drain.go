package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"
)

type waitDrainCommand struct {
	cmd *cobra.Command
}

func newWaitDrainCommand() *waitDrainCommand {
	waitDrainCommand := &waitDrainCommand{}
	waitDrainCommand.cmd = &cobra.Command{
		Use:       "wait-drain <drain-id>",
		Short:     "Wait for an asynchronous deployment's old requests to finish",
		RunE:      waitDrainCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"drain-id"},
	}
	return waitDrainCommand
}

func (c *waitDrainCommand) run(cmd *cobra.Command, args []string) error {
	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.WaitDrain", args[0], &response)
	})
}
