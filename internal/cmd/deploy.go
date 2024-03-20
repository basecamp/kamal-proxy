package cmd

import (
	"fmt"
	"net/rpc"
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
	tlsStaging        bool
}

func newDeployCommand() *deployCommand {
	deployCommand := &deployCommand{}
	deployCommand.cmd = &cobra.Command{
		Use:       "deploy <target>",
		Short:     "Deploy a target host",
		RunE:      deployCommand.deploy,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"target"},
	}

	deployCommand.cmd.Flags().BoolVar(&deployCommand.tls, "tls", false, "Configure TLS for this target (requires a non-empty host)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.tlsStaging, "tls-staging", false, "Use Let's Encrypt staging environmnent for certificate provisioning")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.addTimeout, "timeout", server.DefaultAddTimeout, "Maximum time to wait for a target to become healthy")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.targetOptions.RequestTimeout, "request-timeout", server.DefaultRequestTimeout, "Maximum time to wait for the target server to respond")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.healthCheckConfig.Interval, "health-check-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.healthCheckConfig.Timeout, "health-check-timeout", server.DefaultHealthCheckTimeout, "Time each health check must complete in")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.targetOptions.MaxRequestBodySize, "max-request-body", 0, "Max size of request body (default of 0 means unlimited)")
	deployCommand.cmd.Flags().StringVar(&deployCommand.healthCheckConfig.Path, "health-check-path", server.DefaultHealthCheckPath, "Path to check for health")
	deployCommand.cmd.Flags().StringVar(&deployCommand.host, "host", "", "Host to serve this target on (empty for wildcard)")

	return deployCommand
}

func (c *deployCommand) deploy(cmd *cobra.Command, args []string) error {
	targetURL := args[0]

	if c.tls && c.host == "" {
		return fmt.Errorf("host must be set when using TLS")
	}

	if c.tls {
		c.targetOptions.ACMECachePath = globalConfig.CertificatePath()
		c.targetOptions.TLSHostname = c.host
	}

	if c.tlsStaging {
		c.targetOptions.ACMEDirectory = server.ACMEStagingDirectoryURL
	}

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		args := server.DeployArgs{
			Host:              c.host,
			TargetURL:         targetURL,
			Timeout:           c.addTimeout,
			HealthCheckConfig: c.healthCheckConfig,
			TargetOptions:     c.targetOptions,
		}

		return client.Call("mproxy.Deploy", args, &response)
	})
}
