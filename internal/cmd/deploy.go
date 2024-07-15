package cmd

import (
	"fmt"
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type deployCommand struct {
	cmd  *cobra.Command
	args server.DeployArgs

	tls        bool
	tlsStaging bool
}

func newDeployCommand() *deployCommand {
	deployCommand := &deployCommand{}
	deployCommand.cmd = &cobra.Command{
		Use:       "deploy <service>",
		Short:     "Deploy a target host",
		RunE:      deployCommand.deploy,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	deployCommand.cmd.Flags().StringVar(&deployCommand.args.TargetURL, "target", "", "Target host to deploy")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.Host, "host", "", "Host to serve this target on (empty for wildcard)")

	deployCommand.cmd.Flags().BoolVar(&deployCommand.tls, "tls", false, "Configure TLS for this target (requires a non-empty host)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.tlsStaging, "tls-staging", false, "Use Let's Encrypt staging environmnent for certificate provisioning")

	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.DeployTimeout, "deploy-timeout", server.DefaultDeployTimeout, "Maximum time to wait for the new target to become healthy")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "Maximum time to allow existing connections to drain before removing old target")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.ServiceOptions.HealthCheckConfig.Interval, "health-check-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.ServiceOptions.HealthCheckConfig.Timeout, "health-check-timeout", server.DefaultHealthCheckTimeout, "Time each health check must complete in")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.ServiceOptions.HealthCheckConfig.Path, "health-check-path", server.DefaultHealthCheckPath, "Path to check for health")

	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.ServiceOptions.TargetTimeout, "target-timeout", server.DefaultTargetTimeout, "Maximum time to wait for the target server to respond when serving requests")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.ServiceOptions.MaxRequestMemoryBufferSize, "request-buffer-size", 0, "Enable request buffering in memory (default of 0 means no buffering)")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.ServiceOptions.MaxRequestBodySize, "max-request-body", 0, "Max size of request body (default of 0 means unlimited)")

	deployCommand.cmd.MarkFlagRequired("target")

	return deployCommand
}

func (c *deployCommand) deploy(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	if c.tls && c.args.Host == "" {
		return fmt.Errorf("host must be set when using TLS")
	}

	if c.tls {
		c.args.ServiceOptions.ACMECachePath = globalConfig.CertificatePath()
		c.args.ServiceOptions.TLSHostname = c.args.Host
	}

	if c.tlsStaging {
		c.args.ServiceOptions.ACMEDirectory = server.ACMEStagingDirectoryURL
	}

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool

		return client.Call("kamal-proxy.Deploy", c.args, &response)
	})
}
