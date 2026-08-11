package cmd

import (
	"net/rpc"

	"github.com/basecamp/kamal-proxy/internal/server"
	"github.com/spf13/cobra"
)

type rolloutEnableCommand struct {
	cmd  *cobra.Command
	args server.RolloutEnableArgs
}

func newRolloutEnableCommand(enabled bool) *rolloutEnableCommand {
	rolloutEnableCommand := &rolloutEnableCommand{}
	rolloutEnableCommand.args.Enabled = enabled

	use, short := "enable <service>", "Send traffic to the rollout, using the split it was last set to"
	if !enabled {
		use, short = "disable <service>", "Stop sending traffic to the rollout, remembering its split"
	}

	rolloutEnableCommand.cmd = &cobra.Command{
		Use:       use,
		Short:     short,
		RunE:      rolloutEnableCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	return rolloutEnableCommand
}

func (c *rolloutEnableCommand) run(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.RolloutEnable", c.args, &response)
	})
}
