package cmd

import (
	"fmt"
	"net/rpc"

	"github.com/spf13/cobra"

	"github.com/kevinmcconnell/mproxy/pkg/server"
)

// addCmd represents the add command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List the current contents of the server pool",
	RunE:  listPoolEntries,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func listPoolEntries(cmd *cobra.Command, args []string) error {
	return withRPCClient(func(client *rpc.Client) error {
		var response server.ListResponse
		err := client.Call("mproxy.List", true, &response)
		if err != nil {
			return err
		}

		for _, host := range response.Hosts {
			fmt.Println(host)
		}

		return nil
	})
}
