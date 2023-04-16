package server

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"
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
		log.Err(err).Msg("Unable to create healthcheck request")
		hc.consumer.HealthCheckCompleted(false)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Info().Err(err).Msg("Healthcheck failed")
		hc.consumer.HealthCheckCompleted(false)
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Info().Int("status", resp.StatusCode).Msg("Healthcheck failed")
		hc.consumer.HealthCheckCompleted(false)
		return
	}

	hc.consumer.HealthCheckCompleted(true)
}
