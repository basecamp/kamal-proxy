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

	rolloutDeployCommand.cmd.Flags().StringSliceVar(&rolloutDeployCommand.args.TargetURLs, "target", []string{}, "Target host(s) to deploy")
	rolloutDeployCommand.cmd.Flags().StringSliceVar(&rolloutDeployCommand.args.ReaderURLs, "read-target", []string{}, "Read-only target host(s) to deploy")
	rolloutDeployCommand.cmd.Flags().DurationVar(&rolloutDeployCommand.args.DeploymentOptions.DeployTimeout, "deploy-timeout", server.DefaultDeployTimeout, "Maximum time to wait for the new target to become healthy")
	rolloutDeployCommand.cmd.Flags().DurationVar(&rolloutDeployCommand.args.DeploymentOptions.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "Maximum time to allow existing connections to drain before removing old target")

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
