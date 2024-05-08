package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/basecamp/parachute/internal/server"
)

type resumeCommand struct {
	cmd  *cobra.Command
	args server.ResumeArgs
}

func newResumeCommand() *resumeCommand {
	resumeCommand := &resumeCommand{}
	resumeCommand.cmd = &cobra.Command{
		Use:       "resume <service>",
		Short:     "Resume a service",
		RunE:      resumeCommand.run,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"service"},
	}

	return resumeCommand
}

func (c *resumeCommand) run(cmd *cobra.Command, args []string) error {
	var response bool

	return withRPCClient(globalConfig.SocketPath(), func(client *rpc.Client) error {
		return client.Call("parachute.Resume", c.args, &response)
	})
}
