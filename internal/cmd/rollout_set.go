package cmd

import (
	"net/rpc"

	"github.com/basecamp/kamal-proxy/internal/server"
	"github.com/spf13/cobra"
)

type rolloutSetCommand struct {
	cmd  *cobra.Command
	args server.RolloutSetArgs
}

func newRolloutSetCommand() *rolloutSetCommand {
	rolloutSetCommand := &rolloutSetCommand{}
	rolloutSetCommand.cmd = &cobra.Command{
		Use:       "set <service>",
		Short:     "Set traffic split for rollout",
		RunE:      rolloutSetCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	rolloutSetCommand.cmd.Flags().IntVar(&rolloutSetCommand.args.Percentage, "percent", 0, "Percentage of traffic to send to the new target")
	rolloutSetCommand.cmd.Flags().StringSliceVar(&rolloutSetCommand.args.Allowlist, "list", []string{}, "Rollout to specific values")

	rolloutSetCommand.cmd.MarkFlagsOneRequired("percent", "list")

	return rolloutSetCommand
}

func (c *rolloutSetCommand) run(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.RolloutSet", c.args, &response)
	})
}
