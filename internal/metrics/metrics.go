// Package metrics provides Prometheus instrumentation for /metrics.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "slack_bot"

var (
	registry = prometheus.NewRegistry()

	socketConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "socket_connected",
		Help:      "Whether the Socket Mode connection is established.",
	})

	ledSendTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "led_send_total",
		Help:      "LED image sends by outcome.",
	}, []string{"result"})

	// Buckets cover LAN latency through the operation timeout.
	ledSendDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "led_send_duration_seconds",
		Help:      "Time spent in a LED image send.",
		Buckets:   prometheus.ExponentialBuckets(0.005, 2, 8),
	})

	messagesRendered = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "messages_rendered_total",
		Help:      "Messages rendered to an image.",
	})

	renderFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "render_failures_total",
		Help:      "Messages that could not be rendered.",
	})
)

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		socketConnected,
		ledSendTotal,
		ledSendDuration,
		messagesRendered,
		renderFailures,
	)

	// Pre-create label values so alerts can evaluate zero samples.
	ledSendTotal.WithLabelValues("ok")
	ledSendTotal.WithLabelValues("error")
}

// SetSocketConnected records the state of the Socket Mode connection.
func SetSocketConnected(connected bool) {
	if connected {
		socketConnected.Set(1)
		return
	}
	socketConnected.Set(0)
}

// ObserveLEDSend records one LED image send and its outcome.
func ObserveLEDSend(d time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	ledSendTotal.WithLabelValues(result).Inc()
	ledSendDuration.Observe(d.Seconds())
}

// IncMessageRendered counts a message rendered to an image.
func IncMessageRendered() {
	messagesRendered.Inc()
}

// IncRenderFailure counts a message that could not be rendered.
func IncRenderFailure() {
	renderFailures.Inc()
}

// Handler serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// Gatherer returns the registry for tests.
func Gatherer() prometheus.Gatherer {
	return registry
}
