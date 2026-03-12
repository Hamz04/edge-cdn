package metrics

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	CacheHitRatio   prometheus.Gauge
	CacheEntries    prometheus.Gauge
	OriginRequests  prometheus.Counter
	ActiveConns     prometheus.Gauge

	nodeName  string
	startTime time.Time
}

// New creates a Metrics instance. Accepts a node name string (Stage 3 API).
func New(nodeName string) *Metrics {
	return &Metrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{Namespace: "edgecdn", Name: "requests_total", Help: "Total HTTP requests."},
			[]string{"method", "path", "status", "cache_status"},
		),
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "edgecdn", Name: "request_duration_seconds", Help: "Request latency distribution.",
				Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
			},
			[]string{"method", "cache_status"},
		),
		CacheHitRatio: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn", Name: "cache_hit_ratio", Help: "Current cache hit ratio (0.0-1.0).",
		}),
		CacheEntries: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn", Name: "cache_entries", Help: "Number of cached entries.",
		}),
		OriginRequests: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn", Name: "origin_requests_total", Help: "Total origin requests.",
		}),
		ActiveConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn", Name: "active_connections", Help: "Active HTTP connections.",
		}),
		nodeName:  nodeName,
		startTime: time.Now(),
	}
}

func (m *Metrics) RecordRequest(method, path, status, cacheStatus string, duration time.Duration) {
	normalizedPath := normalizePath(path)
	m.RequestsTotal.WithLabelValues(method, normalizedPath, status, cacheStatus).Inc()
	m.RequestDuration.WithLabelValues(method, cacheStatus).Observe(duration.Seconds())
	if cacheStatus == "MISS" {
		m.OriginRequests.Inc()
	}
}

func (m *Metrics) PrometheusHandler() http.Handler { return promhttp.Handler() }

func (m *Metrics) StatsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := map[string]interface{}{
			"uptime":         time.Since(m.startTime).String(),
			"uptime_seconds": time.Since(m.startTime).Seconds(),
			"node":           m.nodeName,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(stats)
	}
}

func (m *Metrics) StartGaugeUpdater(interval time.Duration) func() {
	stopCh := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// gauge update placeholder
			case <-stopCh:
				return
			}
		}
	}()
	return func() { once.Do(func() { close(stopCh) }) }
}

func normalizePath(p string) string {
	if p == "/" || p == "" {
		return "/"
	}
	segments := splitPath(p)
	if len(segments) <= 2 {
		return p
	}
	result := ""
	for i, seg := range segments {
		if i < 2 {
			result += "/" + seg
		} else {
			result += "/*"
			break
		}
	}
	return result
}

func splitPath(p string) []string {
	var segments []string
	current := ""
	for _, c := range p {
		if c == '/' {
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		segments = append(segments, current)
	}
	return segments
}
