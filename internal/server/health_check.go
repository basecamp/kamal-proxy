package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	healthCheckUserAgent = "kamal-proxy"
)

var (
	ErrorHealthCheckRequestTimedOut  = errors.New("request timed out")
	ErrorHealthCheckUnexpectedStatus = errors.New("unexpected status")
)

type HealthCheckConsumer interface {
	HealthCheckCompleted(success bool)
}

type HealthCheck struct {
	consumer HealthCheckConsumer
	endpoint *url.URL
	interval time.Duration
	timeout  time.Duration
	host     string

	ctx    context.Context
	cancel context.CancelFunc
}

func NewHealthCheck(consumer HealthCheckConsumer, endpoint *url.URL, interval time.Duration, timeout time.Duration, host string) *HealthCheck {
	ctx, cancel := context.WithCancel(context.Background())

	hc := &HealthCheck{
		consumer: consumer,
		endpoint: endpoint,
		interval: interval,
		timeout:  timeout,
		host:     host,

		ctx:    ctx,
		cancel: cancel,
	}

	go hc.run()
	return hc
}

func (hc *HealthCheck) Close() {
	hc.cancel()
}

// Private

func (hc *HealthCheck) run() {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	hc.check()

	for {
		select {
		case <-hc.ctx.Done():
			return
		case <-ticker.C:
			hc.check()
		}
	}
}

func (hc *HealthCheck) check() {
	ctx, cancel := context.WithTimeout(hc.ctx, hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hc.endpoint.String(), nil)
	if err != nil {
		hc.reportResult(false, err)
		return
	}

	req.Header.Set("User-Agent", healthCheckUserAgent)

	if hc.host != "" {
		req.Host = hc.host
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = ErrorHealthCheckRequestTimedOut
		}
		hc.reportResult(false, err)
		return
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		hc.reportResult(false, fmt.Errorf("%w (%d)", ErrorHealthCheckUnexpectedStatus, resp.StatusCode))
		return
	}

	hc.reportResult(true, nil)
}

func (hc *HealthCheck) reportResult(success bool, err error) {
	if !success {
		slog.Info("Healthcheck failed", "url", hc.endpoint.String(), "error", err)
	}

	hc.consumer.HealthCheckCompleted(success)
}
