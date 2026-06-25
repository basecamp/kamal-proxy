package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

type DockerClient struct {
	httpClient *http.Client
}

func NewDockerClient(socketPath string) *DockerClient {
	return &DockerClient{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *DockerClient) StopContainer(ctx context.Context, name string) error {
	url := fmt.Sprintf("http://localhost/v1.41/containers/%s/stop", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("unexpected status code from docker stop: %d", resp.StatusCode)
	}

	return nil
}

func (c *DockerClient) StartContainer(ctx context.Context, name string) error {
	url := fmt.Sprintf("http://localhost/v1.41/containers/%s/start", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("unexpected status code from docker start: %d", resp.StatusCode)
	}

	return nil
}
