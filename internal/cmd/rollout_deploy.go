package cmd

import (
	"net/rpc"

	"github.com/basecamp/kamal-proxy/internal/server"
	"github.com/spf13/cobra"
)

type rolloutDeployCommand struct {
	cmd  *cobra.Command
	args server.RolloutDeployArgs
}

func newRolloutDeployCommand() *rolloutDeployCommand {
	rolloutDeployCommand := &rolloutDeployCommand{}
	rolloutDeployCommand.cmd = &cobra.Command{
		Use:       "deploy <service>",
		Short:     "Deploy the rollout target",
		RunE:      rolloutDeployCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	rolloutDeployCommand.cmd.Flags().StringVar(&rolloutDeployCommand.args.TargetURL, "target", "", "Target host to deploy")
	rolloutDeployCommand.cmd.Flags().DurationVar(&rolloutDeployCommand.args.DeployTimeout, "deploy-timeout", server.DefaultDeployTimeout, "Maximum time to wait for the new target to become healthy")
	rolloutDeployCommand.cmd.Flags().DurationVar(&rolloutDeployCommand.args.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "Maximum time to allow existing connections to drain before removing old target")

	//nolint:errcheck
	rolloutDeployCommand.cmd.MarkFlagRequired("target")

	return rolloutDeployCommand
}

func (c *rolloutDeployCommand) run(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.RolloutDeploy", c.args, &response)
	})
}
