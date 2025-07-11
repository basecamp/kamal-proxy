package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	"log/slog"

	"golang.org/x/crypto/acme/autocert"
)

type TLSOnDemandChecker struct {
	service *Service
	options ServiceOptions
}

func NewTLSOnDemandChecker(service *Service) *TLSOnDemandChecker {
	return &TLSOnDemandChecker{
		service: service,
		options: service.options,
	}
}

func (c *TLSOnDemandChecker) HostPolicy() (autocert.HostPolicy, error) {
	if c.options.TLSOnDemandUrl == "" {
		return autocert.HostWhitelist(c.options.Hosts...), nil
	}

	// If the URL starts with '/', treat it as a local path
	if len(c.options.TLSOnDemandUrl) > 0 && c.options.TLSOnDemandUrl[0] == '/' {
		return c.LocalHostPolicy(), nil
	}

	// Otherwise, treat as external URL
	_, err := url.ParseRequestURI(c.options.TLSOnDemandUrl)

	if err != nil {
		slog.Error("Unable to parse the tls_on_demand_url URL")
		return nil, err
	}

	return c.ExternalHostPolicy(), nil
}

func (c *TLSOnDemandChecker) LocalHostPolicy() autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		path := c.buildURLOrPath(host)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}

		// We use httptest.NewRecorder here to route the request through the service's
		// load balancer and handler, capturing the response in-memory without making
		// a real network request. This ensures the request is processed as if it were
		// an external client, but avoids network overhead and complexity.
		recorder := httptest.NewRecorder()
		c.service.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusOK {
			body := recorder.Body.String()

			if len(body) > 256 {
				body = body[:256]
			}

			return c.handleError(host, recorder.Code, body)
		}
		return nil
	}
}

func (c *TLSOnDemandChecker) ExternalHostPolicy() autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		client := &http.Client{Timeout: 2 * time.Second}
		url := c.buildURLOrPath(host)
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body := make([]byte, 256)
			n, _ := resp.Body.Read(body)
			bodyStr := string(body[:n])
			return c.handleError(host, resp.StatusCode, bodyStr)
		}
		return nil
	}
}

func (c *TLSOnDemandChecker) buildURLOrPath(host string) string {
	return fmt.Sprintf("%s?host=%s", c.options.TLSOnDemandUrl, url.QueryEscape(host))
}

func (c *TLSOnDemandChecker) handleError(host string, status int, body string) error {
	slog.Warn("TLS on demand denied host", "host", host, "status", status, "body", body)

	return fmt.Errorf("%s is not allowed to get a certificate (status: %d, body: \"%s\")", host, status, body)
}
