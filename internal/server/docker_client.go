package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const (
	legacyDockerAPIVersion = "1.41"
	maxDockerErrorBody     = 4096
)

type DockerClient struct {
	httpClient *http.Client

	versionOnce sync.Once
	apiVersion  string
	versionErr  error
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
	return c.containerAction(ctx, name, "stop")
}

func (c *DockerClient) StartContainer(ctx context.Context, name string) error {
	return c.containerAction(ctx, name, "start")
}

func (c *DockerClient) containerAction(ctx context.Context, name, action string) error {
	version, err := c.negotiatedVersion(ctx)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("http://localhost/v%s/containers/%s/%s", version, url.PathEscape(name), action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		return dockerResponseError(action, resp)
	}
	return nil
}

func (c *DockerClient) negotiatedVersion(ctx context.Context) (string, error) {
	c.versionOnce.Do(func() {
		c.apiVersion, c.versionErr = c.queryVersion(ctx)
	})
	return c.apiVersion, c.versionErr
}

func (c *DockerClient) queryVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Some compatible Docker proxies do not expose /version. Preserve the
		// legacy behavior and let the versioned operation return the useful error.
		return legacyDockerAPIVersion, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return legacyDockerAPIVersion, nil
	}
	var version struct {
		APIVersion string `json:"ApiVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDockerErrorBody+1)).Decode(&version); err != nil {
		return "", fmt.Errorf("invalid docker /version response: %w", err)
	}
	if version.APIVersion == "" {
		return "", errors.New("docker /version response has no ApiVersion")
	}
	return version.APIVersion, nil
}

func dockerResponseError(action string, resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDockerErrorBody+1))
	if err != nil {
		return fmt.Errorf("docker %s returned status %d (reading error body: %w)", action, resp.StatusCode, err)
	}
	truncated := len(body) > maxDockerErrorBody
	if truncated {
		body = body[:maxDockerErrorBody]
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("docker %s returned status %d", action, resp.StatusCode)
	}
	if truncated {
		message += "…"
	}
	return fmt.Errorf("docker %s returned status %d: %s", action, resp.StatusCode, message)
}
