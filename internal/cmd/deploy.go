package cmd

import (
	"fmt"
	"net/rpc"
	"slices"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type deployCommand struct {
	cmd        *cobra.Command
	args       server.DeployArgs
	tlsStaging bool
}

func newDeployCommand() *deployCommand {
	deployCommand := &deployCommand{}
	deployCommand.cmd = &cobra.Command{
		Use:       "deploy <service>",
		Short:     "Deploy a target host",
		PreRunE:   deployCommand.preRun,
		RunE:      deployCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.TargetURLs, "target", []string{}, "Target host(s) to deploy")
	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.ReaderURLs, "read-target", []string{}, "Read-only target host(s) to deploy")
	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.ServiceOptions.Hosts, "host", []string{}, "Host(s) to serve this target on (empty for wildcard)")
	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.ServiceOptions.PathPrefixes, "path-prefix", []string{}, "Deploy the service below the specified path(s)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.ServiceOptions.StripPrefix, "strip-path-prefix", true, "With --path-prefix, strip prefix from request before forwarding")

	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.ServiceOptions.TLSEnabled, "tls", false, "Configure TLS for this target (requires a non-empty host)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.tlsStaging, "tls-staging", false, "Use Let's Encrypt staging environment for certificate provisioning")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.ServiceOptions.TLSCertificatePath, "tls-certificate-path", "", "Configure custom TLS certificate path (PEM format)")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.ServiceOptions.TLSPrivateKeyPath, "tls-private-key-path", "", "Configure custom TLS private key path (PEM format)")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.ServiceOptions.TLSRedirect, "tls-redirect", true, "Redirect HTTP traffic to HTTPS")

	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.DeployTimeout, "deploy-timeout", server.DefaultDeployTimeout, "Maximum time to wait for the new target to become healthy")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.DrainTimeout, "drain-timeout", server.DefaultDrainTimeout, "Maximum time to allow existing connections to drain before removing old target")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Interval, "health-check-interval", server.DefaultHealthCheckInterval, "Interval between health checks")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Timeout, "health-check-timeout", server.DefaultHealthCheckTimeout, "Time each health check must complete in")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Path, "health-check-path", server.DefaultHealthCheckPath, "Path to check for health")
	deployCommand.cmd.Flags().IntVar(&deployCommand.args.TargetOptions.HealthCheckConfig.Port, "health-check-port", server.DefaultHealthCheckPort, "Port to check for health (default matches target port)")
	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.ServiceOptions.WriterAffinityTimeout, "writer-affinity-timeout", server.DefaultWriterAffinityTimeout, "Time after a write before read requests will be routed to readers")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.ServiceOptions.ReadTargetsAcceptWebsockets, "read-target-websockets", false, "Route WebSocket traffic to read targets, when available")

	deployCommand.cmd.Flags().DurationVar(&deployCommand.args.TargetOptions.ResponseTimeout, "target-timeout", server.DefaultTargetTimeout, "Maximum time to wait for the target server to respond when serving requests")

	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.TargetOptions.BufferRequests, "buffer-requests", false, "Buffer requests before forwarding to target")
	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.TargetOptions.BufferResponses, "buffer-responses", false, "Buffer responses before forwarding to client")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxMemoryBufferSize, "buffer-memory", server.DefaultMaxMemoryBufferSize, "Max size of memory buffer")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxRequestBodySize, "max-request-body", server.DefaultMaxRequestBodySize, "Max size of request body when buffering (default of 0 means unlimited)")
	deployCommand.cmd.Flags().Int64Var(&deployCommand.args.TargetOptions.MaxResponseBodySize, "max-response-body", server.DefaultMaxResponseBodySize, "Max size of response body when buffering (default of 0 means unlimited)")
	deployCommand.cmd.Flags().StringVar(&deployCommand.args.ServiceOptions.ErrorPagePath, "error-pages", "", "Path to custom error pages")

	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.TargetOptions.LogRequestHeaders, "log-request-header", nil, "Additional request header to log (may be specified multiple times)")
	deployCommand.cmd.Flags().StringSliceVar(&deployCommand.args.TargetOptions.LogResponseHeaders, "log-response-header", nil, "Additional response header to log (may be specified multiple times)")

	deployCommand.cmd.Flags().BoolVar(&deployCommand.args.TargetOptions.ForwardHeaders, "forward-headers", false, "Forward X-Forwarded headers to target (default false if TLS enabled; otherwise true)")

	deployCommand.cmd.MarkFlagRequired("target")
	deployCommand.cmd.MarkFlagsRequiredTogether("tls-certificate-path", "tls-private-key-path")

	return deployCommand
}

func (c *deployCommand) run(cmd *cobra.Command, args []string) error {
	c.args.Service = args[0]

	if c.args.ServiceOptions.TLSEnabled {
		c.args.ServiceOptions.ACMECachePath = globalConfig.CertificatePath()

		if c.tlsStaging {
			c.args.ServiceOptions.ACMEDirectory = server.ACMEStagingDirectoryURL
		}
	}

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		var response bool
		return client.Call("kamal-proxy.Deploy", c.args, &response)
	})
}

func (c *deployCommand) preRun(cmd *cobra.Command, args []string) error {
	c.args.ServiceOptions.Normalize()

	if cmd.Flags().Changed("max-request-body") && !cmd.Flags().Changed("buffer-requests") {
		return fmt.Errorf("max-request-body can only be set when request buffering is enabled")
	}

	if cmd.Flags().Changed("max-response-body") && !cmd.Flags().Changed("buffer-responses") {
		return fmt.Errorf("max-response-body can only be set when response buffering is enabled")
	}

	if !cmd.Flags().Changed("forward-headers") {
		c.args.TargetOptions.ForwardHeaders = !c.args.ServiceOptions.TLSEnabled
	}

	if c.args.ServiceOptions.TLSEnabled {
		if len(c.args.ServiceOptions.Hosts) == 0 {
			return fmt.Errorf("host must be set when using TLS")
		}

		if !slices.Contains(c.args.ServiceOptions.PathPrefixes, "/") {
			return fmt.Errorf("TLS settings must be specified on the root path service")
		}
	}

	return nil
}
