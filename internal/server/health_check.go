package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	healthCheckUserAgent = "kamal-proxy"
)

var ErrorHealthCheckRequestTimedOut = errors.New("Healthcheck request timed out")

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
		case <-hc.shutdown:
			return
		case <-ticker.C:
			select {
			case <-hc.shutdown: // Prioritize shutdown over check
				return
			default:
				hc.check()
			}

		}
	}
}

func (hc *HealthCheck) check() {
	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hc.endpoint.String(), nil)
	if err != nil {
		hc.reportResult(false, 0, err)
		return
	}

	req.Header.Set("User-Agent", healthCheckUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = ErrorHealthCheckRequestTimedOut
		}
		hc.reportResult(false, 0, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		hc.reportResult(false, resp.StatusCode, nil)
		return
	}

	hc.reportResult(true, resp.StatusCode, nil)
}

func (hc *HealthCheck) reportResult(success bool, statusCode int, err error) {
	select {
	case <-hc.shutdown:
		return // Ignore late results after close
	default:
		if success {
			slog.Info("Healthcheck succeeded")
		} else if err != nil {
			slog.Info("Healthcheck failed", "error", err)
		} else {
			slog.Info("Healthcheck failed", "status", statusCode)
		}

		hc.consumer.HealthCheckCompleted(success)
	}
}
