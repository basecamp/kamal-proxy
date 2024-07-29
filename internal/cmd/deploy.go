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
		PreRunE:   deployCommand.preRun,
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
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Interval, "health-check-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Timeout, "health-check-timeout", server.DefaultHealthCheckTimeout, "Time each health check must complete in")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Path, "health-check-path", server.DefaultHealthCheckPath, "Path to check for health")

	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.ResponseTimeout, "target-timeout", server.DefaultTargetTimeout, "Maximum time to wait for the target server to respond when serving requests")

	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.TargetOptions.BufferRequests, "buffer-requests", false, "Buffer requests before forwarding to target")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.TargetOptions.BufferResponses, "buffer-responses", false, "Buffer responses before forwarding to client")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxMemoryBufferSize, "buffer-memory", server.DefaultMaxMemoryBufferSize, "Max size of memory buffer")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxRequestBodySize, "max-request-body", server.DefaultMaxRequestBodySize, "Max size of request body when buffering (default of 0 means unlimited)")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxResponseBodySize, "max-response-body", server.DefaultMaxResponseBodySize, "Max size of response body when buffering (default of 0 means unlimited)")

	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.TargetOptions.LogRequestHeaders, "log-request-header", nil, "Additional request header to log (may be specified multiple times)")
	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.TargetOptions.LogResponseHeaders, "log-response-header", nil, "Additional response header to log (may be specified multiple times)")

	deployCommand.cmd.MarkFlagRequired("target")

	return deployCommand
}

func (c *deployCommand) deploy(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

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

func (c *deployCommand) preRun(cmd *cobra.Command, args []string) error {
	if cmd.Flags().Changed("max-request-body") && !cmd.Flags().Changed("buffer-requests") {
		return fmt.Errorf("max-request-body can only be set when request buffering is enabled")
	}

	if cmd.Flags().Changed("max-response-body") && !cmd.Flags().Changed("buffer-responses") {
		return fmt.Errorf("max-response-body can only be set when response buffering is enabled")
	}

	if cmd.Flags().Changed("tls") && !cmd.Flags().Changed("host") {
		return fmt.Errorf("host must be set when using TLS")
	}

	return nil
}
