package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Config controls health checking behavior. FailThreshold and SuccessThreshold
// prevent flapping — a node must fail N consecutive checks to be marked down,
// and pass N consecutive checks to be marked up. This avoids oscillation from
// transient network blips.
type Config struct {
	Interval         time.Duration
	Timeout          time.Duration
	FailThreshold    int
	SuccessThreshold int
	OnTransition     func(address, region string, healthy bool)
}

// NodeHealth tracks the health state of a single edge node.
type NodeHealth struct {
	Address      string        `json:"address"`
	Region       string        `json:"region"`
	IsHealthy    bool          `json:"is_healthy"`
	LastCheck    time.Time     `json:"last_check"`
	LastLatency  time.Duration `json:"last_latency_ms"`
	FailCount    int           `json:"fail_count"`
	SuccessCount int           `json:"success_count"`
	TotalChecks  int64         `json:"total_checks"`
	TotalFails   int64         `json:"total_fails"`
}

// Checker performs periodic health probes against all registered edge nodes.
// It runs a single background goroutine that sweeps through every node on
// each tick, issuing HTTP GET /health with a configurable timeout.
type Checker struct {
	mu     sync.RWMutex
	nodes  map[string]*NodeHealth
	config Config
	logger *slog.Logger
	client *http.Client
	cancel context.CancelFunc
	done   chan struct{}

	// Prometheus
	nodeStatus    *prometheus.GaugeVec
	checkDuration *prometheus.HistogramVec
	failoverTotal prometheus.Counter
}

func New(cfg Config, logger *slog.Logger) *Checker {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Second
	}
	if cfg.FailThreshold <= 0 {
		cfg.FailThreshold = 3
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}

	return &Checker{
		nodes:  make(map[string]*NodeHealth),
		config: cfg,
		logger: logger,
		client: &http.Client{Timeout: cfg.Timeout},
		done:   make(chan struct{}),
		nodeStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "edgecdn",
				Name:      "node_health_status",
				Help:      "Health status per edge node (1=up, 0=down).",
			},
			[]string{"node", "region"},
		),
		checkDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "edgecdn",
				Name:      "health_check_duration_seconds",
				Help:      "Latency of health check probes.",
				Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0},
			},
			[]string{"node", "region"},
		),
		failoverTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "failover_events_total",
			Help:      "Total number of failover events (node went unhealthy).",
		}),
	}
}

// RegisterNode adds an edge node to the health check pool.
func (c *Checker) RegisterNode(address, region string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nodes[address]; exists {
		return
	}

	c.nodes[address] = &NodeHealth{
		Address:   address,
		Region:    region,
		IsHealthy: true, // optimistic start
		LastCheck: time.Time{},
	}
	c.nodeStatus.WithLabelValues(address, region).Set(1)
	c.logger.Info("registered node for health checks", "node", address, "region", region)
}

// UnregisterNode removes a node from the health check pool.
func (c *Checker) UnregisterNode(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if node, exists := c.nodes[address]; exists {
		c.nodeStatus.DeleteLabelValues(address, node.Region)
		c.checkDuration.DeleteLabelValues(address, node.Region)
		delete(c.nodes, address)
		c.logger.Info("unregistered node from health checks", "node", address)
	}
}

// Start launches the background health check loop. It probes every registered
// node once per interval. Each probe is a simple HTTP GET to /health — if the
// response comes back 200 within the timeout window, the node is considered healthy.
func (c *Checker) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	go func() {
		defer close(c.done)
		ticker := time.NewTicker(c.config.Interval)
		defer ticker.Stop()

		// Run an immediate check on startup so we don't wait a full interval.
		c.checkAll(ctx)

		for {
			select {
			case <-ticker.C:
				c.checkAll(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	c.logger.Info("health checker started",
		"interval", c.config.Interval,
		"timeout", c.config.Timeout,
		"fail_threshold", c.config.FailThreshold,
		"success_threshold", c.config.SuccessThreshold,
	)
}

// Stop gracefully shuts down the health check loop.
func (c *Checker) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	c.logger.Info("health checker stopped")
}

// IsHealthy returns whether a specific node is currently considered healthy.
func (c *Checker) IsHealthy(address string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if node, ok := c.nodes[address]; ok {
		return node.IsHealthy
	}
	return false
}

// GetNodeHealth returns the full health state for a single node.
func (c *Checker) GetNodeHealth(address string) (NodeHealth, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if node, ok := c.nodes[address]; ok {
		return *node, true
	}
	return NodeHealth{}, false
}

// GetAllHealth returns a snapshot of every node's health state.
func (c *Checker) GetAllHealth() map[string]NodeHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]NodeHealth, len(c.nodes))
	for addr, node := range c.nodes {
		result[addr] = *node
	}
	return result
}

// HealthyNodes returns the addresses of all currently healthy nodes.
func (c *Checker) HealthyNodes() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	healthy := make([]string, 0, len(c.nodes))
	for addr, node := range c.nodes {
		if node.IsHealthy {
			healthy = append(healthy, addr)
		}
	}
	return healthy
}

// Handler returns an HTTP handler that exposes node health as JSON.
// Mounted at /health/nodes on the gateway.
func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allHealth := c.GetAllHealth()

		type nodeInfo struct {
			Address     string `json:"address"`
			Region      string `json:"region"`
			Healthy     bool   `json:"healthy"`
			LastCheck   string `json:"last_check"`
			LastLatency string `json:"last_latency"`
			FailCount   int    `json:"fail_count"`
		}

		nodes := make([]nodeInfo, 0, len(allHealth))
		healthyCount := 0
		for _, nh := range allHealth {
			if nh.IsHealthy {
				healthyCount++
			}
			lastCheck := "never"
			if !nh.LastCheck.IsZero() {
				lastCheck = nh.LastCheck.UTC().Format(time.RFC3339)
			}
			nodes = append(nodes, nodeInfo{
				Address:     nh.Address,
				Region:      nh.Region,
				Healthy:     nh.IsHealthy,
				LastCheck:   lastCheck,
				LastLatency: nh.LastLatency.String(),
				FailCount:   nh.FailCount,
			})
		}

		resp := map[string]interface{}{
			"total":   len(nodes),
			"healthy": healthyCount,
			"nodes":   nodes,
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(resp)
	}
}

// checkAll probes every registered node. Each probe runs serially to avoid
// overwhelming the network — with 3-5 nodes and a 2s timeout, worst case
// is 10s which fits comfortably within a 5s interval since most checks
// complete in <50ms.
func (c *Checker) checkAll(ctx context.Context) {
	c.mu.RLock()
	addresses := make([]string, 0, len(c.nodes))
	for addr := range c.nodes {
		addresses = append(addresses, addr)
	}
	c.mu.RUnlock()

	for _, addr := range addresses {
		if ctx.Err() != nil {
			return
		}
		c.checkNode(ctx, addr)
	}
}

// checkNode probes a single node and updates its health state.
func (c *Checker) checkNode(ctx context.Context, address string) {
	start := time.Now()
	healthy := c.probe(ctx, address)
	latency := time.Since(start)

	c.mu.Lock()
	defer c.mu.Unlock()

	node, exists := c.nodes[address]
	if !exists {
		return
	}

	node.LastCheck = time.Now()
	node.LastLatency = latency
	node.TotalChecks++
	c.checkDuration.WithLabelValues(address, node.Region).Observe(latency.Seconds())

	wasHealthy := node.IsHealthy

	if healthy {
		node.FailCount = 0
		node.SuccessCount++
		if !wasHealthy && node.SuccessCount >= c.config.SuccessThreshold {
			node.IsHealthy = true
			c.nodeStatus.WithLabelValues(address, node.Region).Set(1)
			c.logger.Info("node recovered",
				"node", address,
				"region", node.Region,
				"after_checks", node.TotalChecks,
			)
			if c.config.OnTransition != nil {
				// Release lock before callback to avoid deadlock with region router.
				addr, reg := node.Address, node.Region
				c.mu.Unlock()
				c.config.OnTransition(addr, reg, true)
				c.mu.Lock()
			}
		}
	} else {
		node.SuccessCount = 0
		node.FailCount++
		node.TotalFails++
		if wasHealthy && node.FailCount >= c.config.FailThreshold {
			node.IsHealthy = false
			c.nodeStatus.WithLabelValues(address, node.Region).Set(0)
			c.failoverTotal.Inc()
			c.logger.Warn("node marked unhealthy",
				"node", address,
				"region", node.Region,
				"consecutive_failures", node.FailCount,
			)
			if c.config.OnTransition != nil {
				addr, reg := node.Address, node.Region
				c.mu.Unlock()
				c.config.OnTransition(addr, reg, false)
				c.mu.Lock()
			}
		}
	}
}

// probe sends an HTTP GET to the node's /health endpoint.
// Returns true if the response is 200 OK within the timeout.
func (c *Checker) probe(ctx context.Context, address string) bool {
	url := fmt.Sprintf("http://%s/health", address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "EdgeCDN-HealthChecker/2.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// AddTarget registers a node for health checking using its name and health URL.
func (c *Checker) AddTarget(name, healthURL string) {
	c.RegisterNode(name, "default")
}

// StatusHandler returns an HTTP handler that exposes node health as JSON.
func (c *Checker) StatusHandler() http.HandlerFunc {
	return c.Handler()
}
