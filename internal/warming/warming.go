package warming

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamz04/edge-cdn/internal/cache"
	"github.com/Hamz04/edge-cdn/internal/origin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Config struct {
	WarmInterval       time.Duration
	MaxPrefetchPaths   int
	PrefetchThreshold  int
	TrackingWindow     time.Duration
	PrefetchTimeout    time.Duration
	MaxConcurrentFetch int
}

func DefaultConfig() Config {
	return Config{
		WarmInterval:       30 * time.Second,
		MaxPrefetchPaths:   100,
		PrefetchThreshold:  3,
		TrackingWindow:     5 * time.Minute,
		PrefetchTimeout:    10 * time.Second,
		MaxConcurrentFetch: 5,
	}
}

type pathAccess struct {
	path      string
	count     int64
	lastSeen  time.Time
	ttl       time.Duration
}

type Warmer struct {
	cache  cache.Cache
	origin *origin.Server
	cfg    Config
	logger *slog.Logger

	mu       sync.RWMutex
	accesses map[string]*pathAccess
	stopCh   chan struct{}
	once     sync.Once

	prefetchTotal   prometheus.Counter
	prefetchErrors  prometheus.Counter
	prefetchLatency prometheus.Histogram
	trackedPaths    prometheus.Gauge
	warmCycles      prometheus.Counter
	isRunning       atomic.Bool
}

func New(c cache.Cache, o *origin.Server, cfg Config, logger *slog.Logger) *Warmer {
	if cfg.WarmInterval <= 0 {
		cfg.WarmInterval = 30 * time.Second
	}
	if cfg.MaxPrefetchPaths <= 0 {
		cfg.MaxPrefetchPaths = 100
	}
	if cfg.PrefetchThreshold <= 0 {
		cfg.PrefetchThreshold = 3
	}
	if cfg.TrackingWindow <= 0 {
		cfg.TrackingWindow = 5 * time.Minute
	}
	if cfg.PrefetchTimeout <= 0 {
		cfg.PrefetchTimeout = 10 * time.Second
	}
	if cfg.MaxConcurrentFetch <= 0 {
		cfg.MaxConcurrentFetch = 5
	}

	return &Warmer{
		cache:    c,
		origin:   o,
		cfg:      cfg,
		logger:   logger,
		accesses: make(map[string]*pathAccess),
		stopCh:   make(chan struct{}),
		prefetchTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "warming_prefetch_total",
			Help:      "Total cache warming prefetch operations.",
		}),
		prefetchErrors: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "warming_prefetch_errors_total",
			Help:      "Total failed prefetch operations.",
		}),
		prefetchLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "edgecdn",
			Name:      "warming_prefetch_duration_seconds",
			Help:      "Latency of prefetch operations.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		}),
		trackedPaths: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn",
			Name:      "warming_tracked_paths",
			Help:      "Number of paths currently tracked for warming.",
		}),
		warmCycles: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "warming_cycles_total",
			Help:      "Total warming cycles executed.",
		}),
	}
}

func (w *Warmer) RecordAccess(path string, ttl time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if acc, ok := w.accesses[path]; ok {
		acc.count++
		acc.lastSeen = time.Now()
		acc.ttl = ttl
	} else {
		w.accesses[path] = &pathAccess{
			path:     path,
			count:    1,
			lastSeen: time.Now(),
			ttl:      ttl,
		}
	}
}

func (w *Warmer) Start() {
	if w.isRunning.Load() {
		return
	}
	w.isRunning.Store(true)

	go func() {
		ticker := time.NewTicker(w.cfg.WarmInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				w.warmCycle()
			case <-w.stopCh:
				return
			}
		}
	}()

	w.logger.Info("cache warmer started",
		"interval", w.cfg.WarmInterval,
		"max_paths", w.cfg.MaxPrefetchPaths,
		"threshold", w.cfg.PrefetchThreshold,
	)
}

func (w *Warmer) Stop() {
	w.once.Do(func() {
		close(w.stopCh)
		w.isRunning.Store(false)
		w.logger.Info("cache warmer stopped")
	})
}

func (w *Warmer) warmCycle() {
	w.warmCycles.Inc()
	w.pruneOldEntries()
	hotPaths := w.getHotPaths()

	if len(hotPaths) == 0 {
		return
	}

	sem := make(chan struct{}, w.cfg.MaxConcurrentFetch)
	var wg sync.WaitGroup

	for _, pa := range hotPaths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p pathAccess) {
			defer wg.Done()
			defer func() { <-sem }()
			w.prefetch(p)
		}(pa)
	}

	wg.Wait()

	w.trackedPaths.Set(float64(w.pathCount()))
}

func (w *Warmer) prefetch(pa pathAccess) {
	start := time.Now()
	w.prefetchTotal.Inc()

	ctx, cancel := context.WithTimeout(context.Background(), w.cfg.PrefetchTimeout)
	defer cancel()

	resp, err := w.origin.Fetch(pa.path)
	if err != nil {
		w.prefetchErrors.Inc()
		w.logger.Debug("prefetch failed", "path", pa.path, "error", err)
		return
	}

	cacheKey := "cdn:GET:" + pa.path
	ttl := pa.ttl
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}

	if err := w.cache.Set(ctx, cacheKey, resp.Body, ttl); err != nil {
		w.prefetchErrors.Inc()
		w.logger.Debug("prefetch cache set failed", "path", pa.path, "error", err)
		return
	}

	w.prefetchLatency.Observe(time.Since(start).Seconds())
	w.logger.Debug("prefetched", "path", pa.path, "size", len(resp.Body), "ttl", ttl)
}

func (w *Warmer) getHotPaths() []pathAccess {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var hot []pathAccess
	for _, acc := range w.accesses {
		if acc.count >= int64(w.cfg.PrefetchThreshold) {
			hot = append(hot, *acc)
		}
	}

	sort.Slice(hot, func(i, j int) bool {
		return hot[i].count > hot[j].count
	})

	if len(hot) > w.cfg.MaxPrefetchPaths {
		hot = hot[:w.cfg.MaxPrefetchPaths]
	}

	return hot
}

func (w *Warmer) pruneOldEntries() {
	w.mu.Lock()
	defer w.mu.Unlock()

	cutoff := time.Now().Add(-w.cfg.TrackingWindow)
	for key, acc := range w.accesses {
		if acc.lastSeen.Before(cutoff) {
			delete(w.accesses, key)
		}
	}
}

func (w *Warmer) pathCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.accesses)
}

func (w *Warmer) GetHotPathsList() []string {
	hot := w.getHotPaths()
	paths := make([]string, len(hot))
	for i, h := range hot {
		paths[i] = h.path
	}
	return paths
}

func (w *Warmer) TrackedPathCount() int {
	return w.pathCount()
}
