package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Hamz04/edge-cdn/internal/cache"
	"github.com/Hamz04/edge-cdn/internal/circuitbreaker"
	"github.com/Hamz04/edge-cdn/internal/gateway"
	"github.com/Hamz04/edge-cdn/internal/hashing"
	"github.com/Hamz04/edge-cdn/internal/health"
	"github.com/Hamz04/edge-cdn/internal/metrics"
	"github.com/Hamz04/edge-cdn/internal/origin"
	"github.com/Hamz04/edge-cdn/internal/ratelimit"
	"github.com/Hamz04/edge-cdn/internal/region"
	"github.com/Hamz04/edge-cdn/internal/retry"
	"github.com/Hamz04/edge-cdn/internal/router"
	"github.com/Hamz04/edge-cdn/internal/shield"
	"github.com/Hamz04/edge-cdn/internal/warming"
)

func main() {
	// --- Logger setup ---
	logLevel := slog.LevelInfo
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		switch strings.ToLower(envLevel) {
		case "debug":
			logLevel = slog.LevelDebug
		case "warn":
			logLevel = slog.LevelWarn
		case "error":
			logLevel = slog.LevelError
		}
	}

	var logger *slog.Logger
	if os.Getenv("LOG_FORMAT") == "json" {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	}

	logger.Info("starting edge-cdn",
		"version", "2.0.0",
		"node", getEnv("NODE_NAME", "edge-01"),
		"region", getEnv("NODE_REGION", "us-east"),
	)

	// --- Determine mode: edge node or gateway ---
	isGateway := os.Getenv("IS_GATEWAY") == "true"

	var handler http.Handler
	var cleanups []func()

	if isGateway {
		handler, cleanups = startGateway(logger)
	} else {
		handler, cleanups = startEdgeNode(logger)
	}

	// --- HTTP server ---
	port := getEnv("PORT", "8080")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// --- Graceful shutdown ---
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server listening", "port", port, "mode", modeString(isGateway))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stopCh
	logger.Info("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	// Run cleanup functions (stop warmer, rate limiter, health checker, etc.).
	for _, fn := range cleanups {
		fn()
	}

	logger.Info("shutdown complete")
}

func startEdgeNode(logger *slog.Logger) (http.Handler, []func()) {
	var cleanups []func()

	// --- Read configuration from environment ---
	nodeName := getEnv("NODE_NAME", "edge-01")
	nodeRegion := getEnv("NODE_REGION", "us-east")
	redisURL := getEnv("REDIS_URL", "localhost:6379")
	cacheNodes := strings.Split(getEnv("CACHE_NODES", "node-1,node-2,node-3"), ",")
	defaultTTL := getDurationEnv("DEFAULT_TTL_SECONDS", 300) * time.Second
	vnodeCount := getIntEnv("VNODE_COUNT", 150)
	lruSize := getIntEnv("LRU_FALLBACK_SIZE", 10000)
	rateRPS := getIntEnv("RATE_LIMIT_RPS", 100)
	rateBurst := getIntEnv("RATE_LIMIT_BURST", 200)
	cbFailThreshold := getIntEnv("CB_FAIL_THRESHOLD", 5)
	cbOpenTimeout := getDurationEnv("CB_OPEN_TIMEOUT_SEC", 30) * time.Second

	logger.Info("edge node config",
		"node", nodeName,
		"region", nodeRegion,
		"redis", redisURL,
		"cache_nodes", cacheNodes,
		"ttl", defaultTTL,
		"vnodes", vnodeCount,
		"rate_rps", rateRPS,
		"rate_burst", rateBurst,
		"cb_threshold", cbFailThreshold,
		"cb_timeout", cbOpenTimeout,
	)

	// --- Core components ---
	ring := hashing.NewRing(vnodeCount)
	for _, node := range cacheNodes {
		ring.AddNode(strings.TrimSpace(node))
	}

	cdnMetrics := metrics.New(nodeName)
	originServer := origin.New(origin.Config{
		BaseURL: getEnv("ORIGIN_URL", "http://origin:9090"),
		Timeout: 10 * time.Second,
	})

	cdnCache := cache.New(cache.Config{
		RedisURL:  redisURL,
		LRUSize:   lruSize,
		DefaultTTL: defaultTTL,
	}, logger)

	// --- Resiliency components ---

	// Rate limiter: per-IP token bucket.
	rateLimiter := ratelimit.New(rateRPS, rateBurst)
	cleanups = append(cleanups, rateLimiter.Stop)

	// Circuit breaker: protects origin from cascading failures.
	_ = circuitbreaker.New(circuitbreaker.Config{
		FailThreshold:         cbFailThreshold,
		SuccessThreshold:      2,
		OpenTimeout:           cbOpenTimeout,
		HalfOpenMaxConcurrent: 2,
		Name:                  "origin",
	})

	// Retry: exponential backoff with jitter for origin fetches.
	_ = retry.New(retry.DefaultConfig("origin"))

	// Origin shield: request coalescing to prevent thundering herd.
	originShield := shield.New(originServer, shield.DefaultConfig())

	// Cache warmer: prefetch popular paths, proactive refresh.
	warmingCfg := warming.DefaultConfig()
	warmer := warming.New(cdnCache, originServer, warmingCfg, logger)
	warmer.Start()
	cleanups = append(cleanups, warmer.Stop)

	logger.Info("resiliency stack initialized",
		"rate_limiter", fmt.Sprintf("%d rps / %d burst", rateRPS, rateBurst),
		"circuit_breaker", fmt.Sprintf("threshold=%d timeout=%s", cbFailThreshold, cbOpenTimeout),
		"shield", "enabled",
		"warming", "enabled",
	)

	// --- Router ---
	routerCfg := router.Config{
		DefaultTTL:  defaultTTL,
		MaxBodySize: 10 * 1024 * 1024,
		NodeName:    nodeName,
		NodeRegion:  nodeRegion,
	}
	cdnRouter := router.New(routerCfg, ring, cdnCache, originServer, cdnMetrics, logger, originShield, warmer)

	// --- Health checker (for monitoring peer nodes) ---
	healthCfg := health.Config{
		Interval:         5 * time.Second,
		Timeout:          2 * time.Second,
		FailThreshold:    3,
		SuccessThreshold: 2,
	}
	healthChecker := health.New(healthCfg, logger)

	// Register peer nodes for health checking.
	for _, node := range cacheNodes {
		n := strings.TrimSpace(node)
		healthChecker.AddTarget(n, fmt.Sprintf("http://%s:8080/health", n))
	}
	healthChecker.Start()
	cleanups = append(cleanups, healthChecker.Stop)

	cdnRouter.SetHealthHandler(healthChecker.StatusHandler())

	// Wrap with rate limiter middleware.
	return rateLimiter.Middleware(cdnRouter.Handler()), cleanups
}

func startGateway(logger *slog.Logger) (http.Handler, []func()) {
	var cleanups []func()

	edgeNodesRaw := getEnv("EDGE_NODES", "us-east=localhost:8080")

	// Parse region=host pairs.
	type nodeEntry struct {
		region string
		addr   string
		lat    float64
		lon    float64
	}
	var nodes []nodeEntry
	for _, pair := range strings.Split(edgeNodesRaw, ",") {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			continue
		}
		reg := parts[0]
		addr := parts[1]

		// Look up coordinates from region package.
		allRegions := region.DefaultRegions()
		var lat, lon float64
		for _, r := range allRegions {
			if r.Name == reg {
				lat = r.Latitude
				lon = r.Longitude
				break
			}
		}
		nodes = append(nodes, nodeEntry{region: reg, addr: addr, lat: lat, lon: lon})
	}

	if len(nodes) == 0 {
		logger.Error("no edge nodes configured")
		os.Exit(1)
	}

	// Build gateway.
	gatewayCfg := gateway.Config{
		HealthCheckInterval: getDurationEnv("HEALTH_CHECK_INTERVAL_SEC", 5) * time.Second,
		HealthCheckTimeout:  getDurationEnv("HEALTH_CHECK_TIMEOUT_SEC", 2) * time.Second,
		RequestTimeout:      10 * time.Second,
	}

	gw := gateway.NewFromConfig(gatewayCfg, logger)
	for _, n := range nodes {
		gw.AddNode(n.region, n.addr, n.lat, n.lon)
	}
	gw.Start()
	cleanups = append(cleanups, gw.Stop)

	logger.Info("gateway started", "nodes", len(nodes))

	return gw.Handler(), cleanups
}

// --- Helpers ---

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getIntEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getDurationEnv(key string, fallbackSec int) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(fallbackSec)
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return time.Duration(fallbackSec)
	}
	return time.Duration(parsed)
}

func modeString(isGateway bool) string {
	if isGateway {
		return "gateway"
	}
	return "edge"
}
