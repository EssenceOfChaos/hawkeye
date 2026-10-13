package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// APIMetrics are the API process's metrics. They are served on a separate, unpublished port (API_METRICS_PORT), never on the public one.
type APIMetrics struct {
	Registry *prometheus.Registry
	// App holds the application metrics that also occur in the API process (billing webhooks...), on the same registry.
	App *Metrics

	Requests    *prometheus.CounterVec   // method, route, code
	Duration    *prometheus.HistogramVec // route
	InFlight    prometheus.Gauge
	RateLimited *prometheus.CounterVec // scope
	LimiterErrs prometheus.Counter     // the shared limiter's database failed (requests were let through)
	Panics      prometheus.Counter
	EmailSkips  *prometheus.CounterVec // reason: rate_limited
}

func NewAPI() *APIMetrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &APIMetrics{
		Registry: r,
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_http_requests_total", Help: "HTTP requests by method, route template and status code."}, []string{"method", "route", "code"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hawkeye_http_request_duration_seconds", Help: "Request latency by route template.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}}, []string{"route"}),
		InFlight:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_http_requests_in_flight", Help: "Requests currently being served."}),
		RateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_rate_limited_total", Help: "Requests rejected by a rate limit, by scope."}, []string{"scope"}),
		LimiterErrs: prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_rate_limiter_errors_total", Help: "Shared rate limiter failures (fail-open)."}),
		Panics:      prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_http_panics_total", Help: "Panics recovered in request handlers."}),
		EmailSkips:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_email_actions_suppressed_total", Help: "Emails not sent because an address hit its hourly limit."}, []string{"action"}),
	}
	r.MustRegister(m.Requests, m.Duration, m.InFlight, m.RateLimited, m.LimiterErrs, m.Panics, m.EmailSkips)
	m.App = newOn(r)
	return m
}

// Observe records one finished request. route is the registered path template (bounded cardinality), never the raw URL.
func (m *APIMetrics) Observe(method, route string, status int, d time.Duration) {
	if route == "" {
		route = "unmatched"
	}
	m.Requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.Duration.WithLabelValues(route).Observe(d.Seconds())
}

func (m *APIMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// poolCollector exposes pgxpool statistics (connection pool usage and waiting) as Prometheus gauges and counters.
type poolCollector struct {
	pool                                                  *pgxpool.Pool
	acquired, idle, total, max, waiting                   *prometheus.Desc
	acquireCount, emptyAcquire, canceledAcquire, acquireS *prometheus.Desc
}

// RegisterPool adds database connection pool metrics to a registry.
func RegisterPool(r *prometheus.Registry, pool *pgxpool.Pool) {
	d := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc("hawkeye_db_pool_"+name, help, nil, nil)
	}
	r.MustRegister(&poolCollector{pool: pool,
		acquired: d("acquired_connections", "Connections currently in use."), idle: d("idle_connections", "Idle connections."),
		total: d("total_connections", "Open connections."), max: d("max_connections", "Configured maximum."),
		waiting:         d("constructing_connections", "Connections being established."),
		acquireCount:    d("acquires_total", "Total connection acquisitions."),
		emptyAcquire:    d("empty_acquires_total", "Acquisitions that had to wait because no idle connection existed (pool pressure)."),
		canceledAcquire: d("canceled_acquires_total", "Acquisitions cancelled while waiting."),
		acquireS:        d("acquire_wait_seconds_total", "Total time spent waiting for a connection."),
	})
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.waiting, c.acquireCount, c.emptyAcquire, c.canceledAcquire, c.acquireS} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	g := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	ctr := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	g(c.acquired, float64(s.AcquiredConns()))
	g(c.idle, float64(s.IdleConns()))
	g(c.total, float64(s.TotalConns()))
	g(c.max, float64(s.MaxConns()))
	g(c.waiting, float64(s.ConstructingConns()))
	ctr(c.acquireCount, float64(s.AcquireCount()))
	ctr(c.emptyAcquire, float64(s.EmptyAcquireCount()))
	ctr(c.canceledAcquire, float64(s.CanceledAcquireCount()))
	ctr(c.acquireS, s.AcquireDuration().Seconds())
}
