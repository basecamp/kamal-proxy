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
	if c.options.TLSOnDemandURL == "" {
		return autocert.HostWhitelist(c.options.Hosts...), nil
	}

	// If the URL starts with '/', treat it as a local path
	if len(c.options.TLSOnDemandURL) > 0 && c.options.TLSOnDemandURL[0] == '/' {
		return c.LocalHostPolicy(), nil
	}

	// Otherwise, treat as external URL
	_, err := url.ParseRequestURI(c.options.TLSOnDemandURL)

	if err != nil {
		slog.Error("Unable to parse the tls_on_demand_url URL", "error", err, "url", c.options.TLSOnDemandURL)
		return nil, err
	}

	return c.ExternalHostPolicy(), nil
}

func (c *TLSOnDemandChecker) LocalHostPolicy() autocert.HostPolicy {
	return func(ctx context.Context, host string) error {
		path, err := c.buildURLOrPath(host)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
		if err != nil {
			return err
		}

		// We need to set this context value to true to indicate that this is a TLS on demand check
		ctx = context.WithValue(req.Context(), contextKeyTLSOnDemandCheck, true)
		req = req.WithContext(ctx)

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
		requestURL, err := c.buildURLOrPath(host)
		if err != nil {
			return err
		}
		resp, err := client.Get(requestURL)
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

func (c *TLSOnDemandChecker) buildURLOrPath(host string) (string, error) {
	u, err := url.Parse(c.options.TLSOnDemandURL)
	if err != nil {
		return "", err
	}

	query := u.Query()
	query.Set("host", host)
	u.RawQuery = query.Encode()

	return u.String(), nil
}

func (c *TLSOnDemandChecker) handleError(host string, status int, body string) error {
	slog.Warn("TLS on demand denied host", "host", host, "status", status, "body", body)

	return fmt.Errorf("%s is not allowed to get a certificate (status: %d, body: \"%s\")", host, status, body)
}
