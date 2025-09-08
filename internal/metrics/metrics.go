package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type tracker interface {
	TrackRequest(service, method string, status int, duration time.Duration)
	AddInflightRequest(service string)
	SubtractInflightRequest(service string)
}

var Tracker tracker = &nullTracker{}

func Enable() http.Handler {
	Tracker = NewPrometheusTracker()
	return promhttp.Handler()
}

type nullTracker struct{}

func (nullTracker) TrackRequest(service, method string, status int, dur time.Duration) {}
func (nullTracker) AddInflightRequest(service string)                                  {}
func (nullTracker) SubtractInflightRequest(service string)                             {}

type prometheusTracker struct {
	httpRequests     *prometheus.CounterVec
	httpDuration     *prometheus.HistogramVec
	inflightRequests *prometheus.GaugeVec
}

func NewPrometheusTracker() *prometheusTracker {
	tracker := &prometheusTracker{
		httpRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:      "http_requests_total",
				Namespace: "kamal",
				Subsystem: "proxy",
				Help:      "HTTP requests processed, labeled by service, status code and method.",
			},
			[]string{"service", "method", "status"},
		),

		httpDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:      "http_request_duration_seconds",
				Namespace: "kamal",
				Subsystem: "proxy",
				Help:      "Duration of HTTP requests, labeled by service, status code and method.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"service", "method", "status"},
		),

		inflightRequests: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name:      "http_in_flight_requests",
				Namespace: "kamal",
				Subsystem: "proxy",
				Help:      "Number of in-flight HTTP requests, labeled by service.",
			},
			[]string{"service"},
		),
	}

	prometheus.MustRegister(tracker.httpRequests, tracker.httpDuration, tracker.inflightRequests)

	return tracker
}

func (p *prometheusTracker) TrackRequest(service, method string, status int, duration time.Duration) {
	method = normalizeMethod(method)
	statusString := strconv.Itoa(status)

	p.httpRequests.WithLabelValues(service, method, statusString).Inc()
	p.httpDuration.WithLabelValues(service, method, statusString).Observe(duration.Seconds())
}

func (p *prometheusTracker) AddInflightRequest(service string) {
	p.inflightRequests.WithLabelValues(service).Inc()
}

func (p *prometheusTracker) SubtractInflightRequest(service string) {
	p.inflightRequests.WithLabelValues(service).Dec()
}

// Private

func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}
