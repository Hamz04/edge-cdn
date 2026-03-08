package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Hamz04/edge-cdn/internal/health"
	"github.com/Hamz04/edge-cdn/internal/region"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Gateway is the front-door load balancer for the CDN. It receives every
// inbound request, determines the best region based on the client hint
// (X-Region header or ?region= query param), and proxies the request to a
// healthy edge node in that region. If the primary node is down, the gateway
// retries with the next node on the hash ring, then falls back to any
// healthy node in any region.
type Gateway struct {
	regionRouter  *region.Router
	healthChecker *health.Checker
	nodeName      string
	logger        *slog.Logger
	client        *http.Client

	// Metrics
	proxyTotal    *prometheus.CounterVec
	proxyDuration *prometheus.HistogramVec
	failoverCount prometheus.Counter
	activeConns   prometheus.Gauge
	totalRequests atomic.Int64
}

func New(rr *region.Router, hc *health.Checker, nodeName string, logger *slog.Logger) *Gateway {
	return &Gateway{
		regionRouter:  rr,
		healthChecker: hc,
		nodeName:      nodeName,
		logger:        logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Don't follow redirects — pass them through to the client.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		proxyTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "edgecdn",
				Name:      "gateway_proxy_total",
				Help:      "Total proxied requests by region and status.",
			},
			[]string{"region", "status", "failover"},
		),
		proxyDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "edgecdn",
				Name:      "gateway_proxy_duration_seconds",
				Help:      "End-to-end proxy latency including edge node processing.",
				Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
			},
			[]string{"region"},
		),
		failoverCount: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "gateway_failover_total",
			Help:      "Requests that required failover to a different node.",
		}),
		activeConns: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn",
			Name:      "gateway_active_connections",
			Help:      "Currently active proxy connections.",
		}),
	}
}

// Handler returns the full HTTP mux for the gateway.
func (gw *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", gw.handleHealth)
	mux.HandleFunc("/health/nodes", gw.healthChecker.Handler())
	mux.HandleFunc("/regions", gw.handleRegions)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/stats", gw.handleStats)
	mux.HandleFunc("/", gw.handleProxy)
	return mux
}

// handleProxy is the core routing logic. For each request:
// 1. Determine the target region from X-Region header or ?region= param
// 2. Pick the primary node via consistent hashing on the URL path
// 3. Proxy to that node — if it fails, try the next node in the region
// 4. If the whole region is down, fail over to the nearest healthy region
func (gw *Gateway) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := generateID()
	gw.activeConns.Inc()
	defer gw.activeConns.Dec()
	gw.totalRequests.Add(1)

	clientRegion := r.Header.Get("X-Region")
	if clientRegion == "" {
		clientRegion = r.URL.Query().Get("region")
	}

	targetRegion := gw.regionRouter.GetNearestRegion(clientRegion)
	if targetRegion == "" {
		gw.writeError(w, http.StatusServiceUnavailable, "no healthy regions available", requestID)
		gw.proxyTotal.WithLabelValues("none", "503", "false").Inc()
		return
	}

	primaryNode := gw.regionRouter.GetNodeForKey(targetRegion, r.URL.Path)
	failedOver := false

	// Attempt 1: primary node in target region.
	if primaryNode != "" && gw.healthChecker.IsHealthy(primaryNode) {
		if gw.proxyTo(w, r, primaryNode, targetRegion, requestID, failedOver, start) {
			return
		}
	}

	// Attempt 2: any other healthy node in the same region.
	failedOver = true
	gw.failoverCount.Inc()
	regions := gw.regionRouter.GetAllRegions()
	for _, reg := range regions {
		if reg.Name != targetRegion {
			continue
		}
		for _, node := range reg.Nodes {
			if node == primaryNode {
				continue
			}
			if gw.healthChecker.IsHealthy(node) {
				gw.logger.Warn("failing over within region",
					"request_id", requestID,
					"from", primaryNode,
					"to", node,
					"region", targetRegion,
				)
				if gw.proxyTo(w, r, node, targetRegion, requestID, failedOver, start) {
					return
				}
			}
		}
	}

	// Attempt 3: any healthy node in any other region.
	for _, reg := range regions {
		if reg.Name == targetRegion || !reg.Healthy {
			continue
		}
		for _, node := range reg.Nodes {
			if gw.healthChecker.IsHealthy(node) {
				gw.logger.Warn("failing over to different region",
					"request_id", requestID,
					"from_region", targetRegion,
					"to_region", reg.Name,
					"to_node", node,
				)
				if gw.proxyTo(w, r, node, reg.Name, requestID, failedOver, start) {
					return
				}
			}
		}
	}

	// All nodes exhausted.
	gw.writeError(w, http.StatusBadGateway, "all edge nodes unavailable", requestID)
	gw.proxyTotal.WithLabelValues(targetRegion, "502", "true").Inc()
	gw.proxyDuration.WithLabelValues(targetRegion).Observe(time.Since(start).Seconds())
}

// proxyTo forwards the request to a specific edge node and copies the response back.
// Returns true if the proxy succeeded, false if the node is unreachable.
func (gw *Gateway) proxyTo(w http.ResponseWriter, r *http.Request, nodeAddr, regionName, requestID string, failedOver bool, start time.Time) bool {
	targetURL := fmt.Sprintf("http://%s%s", nodeAddr, r.URL.RequestURI())

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		gw.logger.Error("failed to create proxy request", "error", err, "node", nodeAddr)
		return false
	}

	// Forward original headers.
	for key, values := range r.Header {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}
	proxyReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
	proxyReq.Header.Set("X-Request-ID", requestID)

	resp, err := gw.client.Do(proxyReq)
	if err != nil {
		gw.logger.Warn("proxy request failed", "node", nodeAddr, "error", err)
		return false
	}
	defer resp.Body.Close()

	// Copy response headers from edge node.
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	// Add gateway-level headers.
	w.Header().Set("X-Gateway", gw.nodeName)
	w.Header().Set("X-Gateway-Region", regionName)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Gateway-Latency", time.Since(start).String())
	if failedOver {
		w.Header().Set("X-Failover", "true")
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	failoverLabel := "false"
	if failedOver {
		failoverLabel = "true"
	}
	statusStr := fmt.Sprintf("%d", resp.StatusCode)
	gw.proxyTotal.WithLabelValues(regionName, statusStr, failoverLabel).Inc()
	gw.proxyDuration.WithLabelValues(regionName).Observe(time.Since(start).Seconds())

	return true
}

func (gw *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	healthyNodes := gw.healthChecker.HealthyNodes()
	allHealth := gw.healthChecker.GetAllHealth()
	totalNodes := len(allHealth)

	status := "healthy"
	httpStatus := http.StatusOK
	if len(healthyNodes) == 0 {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	} else if len(healthyNodes) < totalNodes {
		status = "partial"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        status,
		"node":          gw.nodeName,
		"role":          "gateway",
		"healthy_nodes": len(healthyNodes),
		"total_nodes":   totalNodes,
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
	})
}

func (gw *Gateway) handleRegions(w http.ResponseWriter, r *http.Request) {
	regions := gw.regionRouter.GetAllRegions()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{
		"regions": regions,
		"total":   len(regions),
	})
}

func (gw *Gateway) handleStats(w http.ResponseWriter, r *http.Request) {
	allHealth := gw.healthChecker.GetAllHealth()
	healthyCount := 0
	for _, nh := range allHealth {
		if nh.IsHealthy {
			healthyCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{
		"gateway":        gw.nodeName,
		"total_requests": gw.totalRequests.Load(),
		"healthy_nodes":  healthyCount,
		"total_nodes":    len(allHealth),
		"regions":        gw.regionRouter.GetAllRegions(),
	})
}

func (gw *Gateway) writeError(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Gateway", gw.nodeName)
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":      message,
		"status":     status,
		"request_id": requestID,
	})
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// inferRegion extracts a region hint from a node address.
// e.g., "edge-us-east:8080" -> "us-east"
func inferRegion(nodeAddr string) string {
	host := nodeAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		host = host[:idx]
	}
	knownRegions := []string{"us-east", "us-west", "eu-west", "eu-central", "ap-south"}
	lower := strings.ToLower(host)
	for _, r := range knownRegions {
		if strings.Contains(lower, r) {
			return r
		}
	}
	return "us-east"
}

// Config is the Stage 3 configuration for the gateway.
type Config struct {
	HealthCheckInterval time.Duration
	HealthCheckTimeout  time.Duration
	RequestTimeout      time.Duration
}

// NewFromConfig creates a Gateway from a Config and logger (Stage 3 API).
// It internally creates a region router and health checker.
func NewFromConfig(cfg Config, logger *slog.Logger) *Gateway {
	rr := region.New()
	hc := health.New(health.Config{
		Interval:         cfg.HealthCheckInterval,
		Timeout:          cfg.HealthCheckTimeout,
		FailThreshold:    3,
		SuccessThreshold: 2,
	}, logger)
	return New(rr, hc, "gateway", logger)
}

// AddNode registers an edge node in the gateway's region router.
func (gw *Gateway) AddNode(regionName, addr string, lat, lon float64) {
	gw.regionRouter.AddNode(regionName, addr)
}

// Start launches the gateway's health checker.
func (gw *Gateway) Start() {
	gw.healthChecker.Start()
}

// Stop shuts down the gateway's health checker.
func (gw *Gateway) Stop() {
	gw.healthChecker.Stop()
}
