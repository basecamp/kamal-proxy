package cmd

import (
	"net/rpc"

	"github.com/spf13/cobra"
)

// addCmd represents the add command
var addCmd = &cobra.Command{
	Use:        "add [flags] host [...host]",
	Short:      "A brief description of your command",
	RunE:       addHosts,
	Args:       cobra.MinimumNArgs(1),
	ArgAliases: []string{"hosts"},
}

func init() {
	rootCmd.AddCommand(addCmd)
}

func addHosts(cmd *cobra.Command, args []string) error {
	hostURLs, err := parseHostURLs(args)
	if err != nil {
		return err
	}

	return withRPCClient(func(client *rpc.Client) error {
		var response bool
		return client.Call("mproxy.AddHosts", hostURLs, &response)
	})
}
