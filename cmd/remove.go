package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"
)

// addCmd represents the add command
var removeCmd = &cobra.Command{
	Use:        "remove [flags] host [...host]",
	Short:      "Remove service instances from proxy",
	RunE:       removeHosts,
	Args:       cobra.MinimumNArgs(1),
	ArgAliases: []string{"hosts"},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

func removeHosts(cmd *cobra.Command, args []string) error {
	hostURLs, err := parseHostURLs(args)
	if err != nil {
		return err
	}

	return withRPCClient(func(client *rpc.Client) error {
		var response bool
		return client.Call("mproxy.RemoveHosts", hostURLs, &response)
	})
}
