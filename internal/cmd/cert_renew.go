package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/kamal-proxy/internal/server"
)

type certRenewCommand struct {
	cmd  *cobra.Command
	args server.CertRenewArgs
}

func newCertRenewCommand() *certRenewCommand {
	certRenewCommand := &certRenewCommand{}
	certRenewCommand.cmd = &cobra.Command{
		Use:   "renew <service>",
		Short: "Renew the automatic TLS certificates of a service",
		Long: `Request new certificates for a service that uses automatic TLS, replacing
the ones currently in use. If a renewal fails, the existing certificate is kept.

Renewing certificates counts towards the rate limits of the certificate
authority, so only use this when a certificate needs to be replaced.`,
		RunE:      certRenewCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	certRenewCommand.cmd.Flags().StringSliceVar(&certRenewCommand.args.Hosts, "host", []string{}, "Host to renew the certificate for (defaults to all of the service's hosts; required for services using a TLS on-demand URL)")

	return certRenewCommand
}

func (c *certRenewCommand) run(cmd *cobra.Command, args []string) error {
	var response bool

	c.args.Service = args[0]

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		return client.Call("kamal-proxy.CertRenew", c.args, &response)
	})
}
