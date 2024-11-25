package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	healthCheckUserAgent = "kamal-proxy"
)

type HealthCheckConsumer interface {
	HealthCheckCompleted(success bool)
}

type HealthCheck struct {
	consumer HealthCheckConsumer
	endpoint *url.URL
	interval time.Duration
	timeout  time.Duration

	shutdown chan (bool)
}

func NewHealthCheck(consumer HealthCheckConsumer, endpoint *url.URL, interval time.Duration, timeout time.Duration) *HealthCheck {
	hc := &HealthCheck{
		consumer: consumer,
		endpoint: endpoint,
		interval: interval,
		timeout:  timeout,

		shutdown: make(chan bool),
	}

	go hc.run()
	return hc
}

func (hc *HealthCheck) Close() {
	close(hc.shutdown)
}

// Private

func (hc *HealthCheck) run() {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	hc.check()

	for {
		select {
		case <-ticker.C:
			hc.check()

		case <-hc.shutdown:
			return
		}
	}
}

func (hc *HealthCheck) check() {
	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hc.endpoint.String(), nil)
	if err != nil {
		slog.Error("Unable to create healthcheck request", "error", err)
		hc.consumer.HealthCheckCompleted(false)
		return
	}

	req.Header.Set("User-Agent", healthCheckUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Info("Healthcheck failed", "error", err)
		hc.consumer.HealthCheckCompleted(false)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		slog.Info("Healthcheck failed", "status", resp.StatusCode)
		hc.consumer.HealthCheckCompleted(false)
		return
	}

	slog.Info("Healthcheck succeeded", "status", resp.StatusCode)
	hc.consumer.HealthCheckCompleted(true)
}
