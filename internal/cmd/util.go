package cmd

import (
	"net/rpc"
)

func withRPCClient(socketPath string, fn func(client *rpc.Client) error) error {
	client, err := rpc.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}
