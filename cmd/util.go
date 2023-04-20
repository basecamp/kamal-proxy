package cmd

import (
	"net/rpc"
)

func withRPCClient(fn func(client *rpc.Client) error) error {
	client, err := rpc.Dial("unix", serverConfig.SocketPath())
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}
