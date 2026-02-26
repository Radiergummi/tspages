package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tspages_http_requests_total",
		Help: "Total HTTP requests by site and status code.",
	}, []string{"site", "status"})

	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tspages_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds by site.",
		Buckets: prometheus.DefBuckets,
	}, []string{"site"})

	deploymentsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tspages_deployments_total",
		Help: "Total deployments by site.",
	}, []string{"site"})

	deploymentSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tspages_deployment_size_bytes",
		Help:    "Deployment upload size in bytes.",
		Buckets: prometheus.ExponentialBuckets(1024, 4, 8), // 1KB → 16GB
	})

	activeSites = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tspages_sites_active",
		Help: "Number of active site servers.",
	})
)

func init() {
	prometheus.MustRegister(
		httpRequests,
		httpDuration,
		deploymentsTotal,
		deploymentSize,
		activeSites,
	)
}

// Handler returns an http.Handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// ObserveRequest records an HTTP request for a site.
func ObserveRequest(site string, status int, duration time.Duration) {
	httpRequests.WithLabelValues(site, strconv.Itoa(status)).Inc()
	httpDuration.WithLabelValues(site).Observe(duration.Seconds())
}

// CountDeploy records a deployment.
func CountDeploy(site string, sizeBytes int64) {
	deploymentsTotal.WithLabelValues(site).Inc()
	deploymentSize.Observe(float64(sizeBytes))
}

// SetActiveSites sets the gauge of active site servers.
func SetActiveSites(n int) {
	activeSites.Set(float64(n))
}
