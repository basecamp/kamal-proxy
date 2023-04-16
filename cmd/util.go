package cmd

import (
	"fmt"
	"net/rpc"
	"net/url"
	"regexp"
)

var hostRegex = regexp.MustCompile(`^(\w[-_.\w+]+)(:\d+)?$`)

func withRPCClient(fn func(client *rpc.Client) error) error {
	client, err := rpc.Dial("unix", globalOptions.socketPath)
	if err != nil {
		return err
	}
	defer client.Close()
	return fn(client)
}

func parseHostURLs(hosts []string) ([]*url.URL, error) {
	hostURLs := []*url.URL{}
	for _, host := range hosts {
		if !hostRegex.MatchString(host) {
			return nil, fmt.Errorf("invalid host pattern: %s", host)
		}

		hostURL, _ := url.Parse("http://" + host)
		hostURLs = append(hostURLs, hostURL)
	}

	return hostURLs, nil
}
