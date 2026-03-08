package router

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hamz04/edge-cdn/internal/cache"
	"github.com/Hamz04/edge-cdn/internal/hashing"
	"github.com/Hamz04/edge-cdn/internal/metrics"
	"github.com/Hamz04/edge-cdn/internal/origin"
	"github.com/Hamz04/edge-cdn/internal/shield"
	"github.com/Hamz04/edge-cdn/internal/warming"
)

type Config struct {
	DefaultTTL  time.Duration
	MaxBodySize int64
	NodeName    string
	NodeRegion  string
}

func DefaultConfig() Config {
	return Config{DefaultTTL: 5 * time.Minute, MaxBodySize: 10 * 1024 * 1024, NodeName: "edge-01", NodeRegion: "us-east"}
}

type Router struct {
	config        Config
	ring          *hashing.Ring
	cache         cache.Cache
	origin        *origin.Server
	metrics       *metrics.Metrics
	logger        *slog.Logger
	healthHandler http.HandlerFunc
	shield        *shield.Shield
	warmer        *warming.Warmer
}

func New(cfg Config, ring *hashing.Ring, c cache.Cache, o *origin.Server, m *metrics.Metrics, logger *slog.Logger, s *shield.Shield, w *warming.Warmer) *Router {
	return &Router{config: cfg, ring: ring, cache: c, origin: o, metrics: m, logger: logger, shield: s, warmer: w}
}

func (rt *Router) SetHealthHandler(handler http.HandlerFunc) { rt.healthHandler = handler }

func (rt *Router) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", rt.handleHealth)
	if rt.healthHandler != nil {
		mux.HandleFunc("/health/nodes", rt.healthHandler)
	}
	mux.Handle("/metrics", rt.metrics.PrometheusHandler())
	mux.HandleFunc("/stats", rt.metrics.StatsHandler())
	mux.HandleFunc("/purge", rt.handlePurge)
	mux.HandleFunc("/", rt.handleCDN)
	return mux
}

func (rt *Router) handleCDN(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := generateRequestID()
	cacheStatus := "MISS"
	rt.metrics.ActiveConns.Inc()
	defer rt.metrics.ActiveConns.Dec()

	ctx := r.Context()
	primaryNode := rt.ring.GetNode(r.URL.Path)
	servingNode := primaryNode
	cacheKey := buildCacheKey(r.Method, r.URL.Path)
	ttl := rt.determineTTL(r.URL.Path)

	rt.logger.Debug("incoming request",
		"request_id", requestID,
		"method", r.Method,
		"path", r.URL.Path,
		"cache_node", primaryNode,
		"region", rt.config.NodeRegion,
	)

	// --- Cache lookup ---
	cachedBody, err := rt.cache.Get(ctx, cacheKey)
	if err == nil {
		cacheStatus = "HIT"
		contentType := detectContentType(r.URL.Path, cachedBody)
		writeResponse(w, http.StatusOK, contentType, cachedBody, map[string]string{
			"X-Cache":         "HIT",
			"X-Cache-Node":    primaryNode,
			"X-Primary-Node":  primaryNode,
			"X-Serving-Node":  servingNode,
			"X-Request-ID":    requestID,
			"X-Response-Time": time.Since(start).String(),
			"X-Node-Name":     rt.config.NodeName,
			"X-Node-Region":   rt.config.NodeRegion,
			"Cache-Control":   fmt.Sprintf("public, max-age=%d", int(rt.config.DefaultTTL.Seconds())),
		})
		rt.metrics.RecordRequest(r.Method, r.URL.Path, "200", cacheStatus, time.Since(start))
		// Track access for warming even on cache hits.
		if rt.warmer != nil {
			rt.warmer.RecordAccess(r.URL.Path, ttl)
		}
		return
	}

	// --- Cache miss: fetch from origin via shield (request coalescing) ---
	var originResp *origin.FetchResponse
	var shielded bool

	if rt.shield != nil {
		originResp, shielded, err = rt.shield.Fetch(ctx, r.URL.Path)
	} else {
		originResp, err = rt.origin.Fetch(r.URL.Path)
	}

	if err != nil {
		rt.logger.Warn("origin fetch failed",
			"request_id", requestID,
			"path", r.URL.Path,
			"error", err,
			"shielded", shielded,
		)
		writeResponse(w, http.StatusBadGateway, "text/plain", []byte("origin server error"), map[string]string{
			"X-Cache":        "ERROR",
			"X-Cache-Node":   primaryNode,
			"X-Primary-Node": primaryNode,
			"X-Serving-Node": servingNode,
			"X-Request-ID":   requestID,
			"X-Node-Name":    rt.config.NodeName,
			"X-Node-Region":  rt.config.NodeRegion,
		})
		rt.metrics.RecordRequest(r.Method, r.URL.Path, "502", "ERROR", time.Since(start))
		return
	}

	// --- Store in cache ---
	if r.Method == http.MethodGet && originResp.StatusCode == http.StatusOK {
		if err := rt.cache.Set(ctx, cacheKey, originResp.Body, ttl); err != nil {
			rt.logger.Warn("failed to cache response", "path", r.URL.Path, "error", err)
		}
	}

	// Track access for warming.
	if rt.warmer != nil {
		rt.warmer.RecordAccess(r.URL.Path, ttl)
	}

	responseHeaders := map[string]string{
		"X-Cache":         "MISS",
		"X-Cache-Node":    primaryNode,
		"X-Primary-Node":  primaryNode,
		"X-Serving-Node":  servingNode,
		"X-Request-ID":    requestID,
		"X-Response-Time": time.Since(start).String(),
		"X-Node-Name":     rt.config.NodeName,
		"X-Node-Region":   rt.config.NodeRegion,
	}
	if shielded {
		responseHeaders["X-Shield"] = "coalesced"
	}

	writeResponse(w, originResp.StatusCode, originResp.ContentType, originResp.Body, responseHeaders)
	rt.metrics.RecordRequest(r.Method, r.URL.Path, strconv.Itoa(originResp.StatusCode), cacheStatus, time.Since(start))
}

func (rt *Router) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy","node":"%s","region":"%s","role":"edge","timestamp":"%s"}`,
		rt.config.NodeName, rt.config.NodeRegion, time.Now().UTC().Format(time.RFC3339))
}

func (rt *Router) handlePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path parameter", http.StatusBadRequest)
		return
	}
	cacheKey := buildCacheKey(http.MethodGet, path)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := rt.cache.Delete(ctx, cacheKey); err != nil {
		http.Error(w, "purge failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"purged":"%s","status":"ok"}`, path)
}

func (rt *Router) determineTTL(urlPath string) time.Duration {
	lower := strings.ToLower(urlPath)
	switch {
	case strings.HasSuffix(lower, ".css"), strings.HasSuffix(lower, ".js"),
		strings.HasSuffix(lower, ".png"), strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".gif"), strings.HasSuffix(lower, ".webp"),
		strings.HasSuffix(lower, ".woff2"), strings.HasSuffix(lower, ".svg"):
		return 1 * time.Hour
	case strings.Contains(lower, "/api/"):
		return 1 * time.Minute
	default:
		return rt.config.DefaultTTL
	}
}

func buildCacheKey(method, urlPath string) string {
	return fmt.Sprintf("cdn:%s:%s", method, urlPath)
}

func generateRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeResponse(w http.ResponseWriter, status int, contentType string, body []byte, headers map[string]string) {
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Server", "EdgeCDN/2.0")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func detectContentType(urlPath string, body []byte) string {
	lower := strings.ToLower(urlPath)
	switch {
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(lower, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	default:
		ct := http.DetectContentType(body)
		if ct != "" {
			return ct
		}
		return "text/html; charset=utf-8"
	}
}
