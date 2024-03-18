package cmd

import (
	"fmt"
	"net/rpc"
	"path"
	"time"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/internal/server"
)

type deployCommand struct {
	cmd               *cobra.Command
	addTimeout        time.Duration
	healthCheckConfig server.HealthCheckConfig
	targetOptions     server.TargetOptions
	host              string
	tls               bool
}

func newDeployCommand() *deployCommand {
	deployCommand := &deployCommand{}
	deployCommand.cmd = &cobra.Command{
		Use:       "deploy <target>",
		Short:     "Deploy a target host",
		RunE:      deployCommand.deployTarget,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"target"},
	}

	deployCommand.cmd.Flags().DurationVar(&deployCommand.addTimeout, "timeout", server.DefaultAddTimeout, "Maximum time to wait for a target to become healthy")
	deployCommand.cmd.Flags().StringVar(&deployCommand.host, "host", "", "Host to serve this target on (empty for wildcard)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.targetOptions.RequireTLS, "tls", false, "Configure TLS for this target (requires a non-empty host)")
	deployCommand.cmd.Flags().StringVar(&deployCommand.healthCheckConfig.Path, "health-check-path", server.DefaultHealthCheckPath, "Path to check for health")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.healthCheckConfig.Interval, "health-check-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.healthCheckConfig.Timeout, "health-check-timeout", server.DefaultHealthCheckTimeout, "Time each health check must complete in")

	return deployCommand
}

func (c *deployCommand) deployTarget(cmd *cobra.Command, args []string) error {
	socketPath := path.Join(configDir, "mproxy.sock") // TODO: move this somewhere shared
	if c.tls && c.host == "" {
		return fmt.Errorf("host must be set when using TLS")
	}

	return c.invoke(socketPath, c.host, args[0], c.addTimeout)
}

func (c *deployCommand) invoke(socketPath string, host string, targetURL string, timeout time.Duration) error {
	return withRPCClient(socketPath, func(client *rpc.Client) error {
		var response bool
		args := server.DeployArgs{
			Host:              host,
			TargetURL:         targetURL,
			Timeout:           timeout,
			HealthCheckConfig: c.healthCheckConfig,
			TargetOptions:     c.targetOptions,
		}

		return client.Call("mproxy.Deploy", args, &response)
	})
}
