package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"
)

// addCmd represents the add command
var deployCmd = &cobra.Command{
	Use:        "deploy [flags] host [...host]",
	Short:      "Deploy service instances to proxy",
	RunE:       deployHosts,
	Args:       cobra.MinimumNArgs(1),
	ArgAliases: []string{"hosts"},
}

func init() {
	rootCmd.AddCommand(deployCmd)
}

func deployHosts(cmd *cobra.Command, args []string) error {
	return withRPCClient(func(client *rpc.Client) error {
		var response bool
		return client.Call("mproxy.DeployHosts", args, &response)
	})
}
