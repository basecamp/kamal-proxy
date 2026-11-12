package cmd

import "github.com/spf13/cobra"

type certCommand struct {
	cmd *cobra.Command
}

func newCertCommand() *certCommand {
	certCommand := &certCommand{}
	certCommand.cmd = &cobra.Command{
		Use:   "cert",
		Short: "Manage TLS certificates",
	}

	certCommand.cmd.AddCommand(newCertRenewCommand().cmd)

	return certCommand
}
