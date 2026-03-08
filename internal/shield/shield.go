package shield

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamz04/edge-cdn/internal/origin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Shield implements origin shielding via request coalescing. When multiple
// concurrent requests arrive for the same cache key, only the first request
// fetches from origin. All other requests wait for and share that single
// response. This prevents the thundering herd problem where a cache miss
// on a popular path causes N simultaneous origin fetches.
//
// This is similar to Go's x/sync/singleflight but purpose-built for CDN
// use with Prometheus metrics and configurable timeouts.
//
// Usage:
//
//	s := shield.New(originServer, shield.DefaultConfig())
//	resp, shared, err := s.Fetch(ctx, "/popular/page.html")
//	// shared == true means this response was coalesced from another request.
type Shield struct {
	origin *origin.Server
	cfg    Config

	mu       sync.Mutex
	inflight map[string]*call

	// Metrics
	fetchTotal     *prometheus.CounterVec
	coalescedTotal prometheus.Counter
	shieldDuration prometheus.Histogram
	infightGauge   prometheus.Gauge
	peakInFlight   atomic.Int64
}

// call represents an in-flight origin fetch that other requests can wait on.
type call struct {
	wg     sync.WaitGroup
	resp   *origin.FetchResponse
	err    error
	shared int64 // number of additional waiters
}

// Config controls shield behavior.
type Config struct {
	// FetchTimeout is the maximum time to wait for an origin fetch.
	FetchTimeout time.Duration
	// MaxWaitersPerKey is the maximum number of requests that can wait on
	// a single in-flight fetch. Beyond this, requests get an error rather
	// than piling up unbounded.
	MaxWaitersPerKey int
}

// DefaultConfig returns production defaults: 10s fetch timeout, 1000 max waiters.
func DefaultConfig() Config {
	return Config{
		FetchTimeout:     10 * time.Second,
		MaxWaitersPerKey: 1000,
	}
}

// New creates an origin shield.
func New(o *origin.Server, cfg Config) *Shield {
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 10 * time.Second
	}
	if cfg.MaxWaitersPerKey <= 0 {
		cfg.MaxWaitersPerKey = 1000
	}

	return &Shield{
		origin:   o,
		cfg:      cfg,
		inflight: make(map[string]*call),
		fetchTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "shield_fetch_total",
			Help:      "Total origin fetches via shield by type.",
		}, []string{"type"}),
		coalescedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "shield_coalesced_total",
			Help:      "Requests that shared a coalesced origin fetch.",
		}),
		shieldDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "edgecdn",
			Name:      "shield_duration_seconds",
			Help:      "Time spent waiting for origin fetch (includes coalesced wait).",
			Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
		}),
		infightGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn",
			Name:      "shield_inflight_keys",
			Help:      "Number of unique keys currently being fetched from origin.",
		}),
	}
}

// Fetch retrieves content for the given path. If another goroutine is already
// fetching the same path, this call waits for that result instead of making
// a duplicate origin request.
//
// Returns the response, whether it was shared (coalesced), and any error.
func (s *Shield) Fetch(ctx context.Context, path string) (*origin.FetchResponse, bool, error) {
	start := time.Now()

	s.mu.Lock()

	// Check if there's already an in-flight request for this path.
	if c, ok := s.inflight[path]; ok {
		// Check waiter limit.
		if atomic.LoadInt64(&c.shared) >= int64(s.cfg.MaxWaitersPerKey) {
			s.mu.Unlock()
			s.fetchTotal.WithLabelValues("rejected").Inc()
			return nil, false, ErrTooManyWaiters
		}
		atomic.AddInt64(&c.shared, 1)
		s.mu.Unlock()

		// Wait for the in-flight request to complete or context to cancel.
		select {
		case <-s.waitDone(c):
			s.coalescedTotal.Inc()
			s.fetchTotal.WithLabelValues("coalesced").Inc()
			s.shieldDuration.Observe(time.Since(start).Seconds())
			return c.resp, true, c.err
		case <-ctx.Done():
			s.fetchTotal.WithLabelValues("context_cancelled").Inc()
			s.shieldDuration.Observe(time.Since(start).Seconds())
			return nil, true, ctx.Err()
		}
	}

	// First request for this path -- we're the leader.
	c := &call{}
	c.wg.Add(1)
	s.inflight[path] = c
	s.infightGauge.Inc()
	s.mu.Unlock()

	// Track peak in-flight.
	current := int64(len(s.inflight))
	if peak := s.peakInFlight.Load(); current > peak {
		s.peakInFlight.Store(current)
	}

	// Perform the actual origin fetch with timeout.
	fetchCtx, fetchCancel := context.WithTimeout(ctx, s.cfg.FetchTimeout)
	defer fetchCancel()

	// Use a channel to bridge the fetch with context.
	type fetchResult struct {
		resp *origin.FetchResponse
		err  error
	}
	resultCh := make(chan fetchResult, 1)
	go func() {
		resp, err := s.origin.Fetch(path)
		resultCh <- fetchResult{resp: resp, err: err}
	}()

	select {
	case result := <-resultCh:
		c.resp = result.resp
		c.err = result.err
	case <-fetchCtx.Done():
		c.err = fetchCtx.Err()
	}

	// Signal all waiters and clean up.
	c.wg.Done()

	s.mu.Lock()
	delete(s.inflight, path)
	s.infightGauge.Dec()
	s.mu.Unlock()

	if c.err != nil {
		s.fetchTotal.WithLabelValues("error").Inc()
	} else {
		s.fetchTotal.WithLabelValues("success").Inc()
	}
	s.shieldDuration.Observe(time.Since(start).Seconds())

	return c.resp, false, c.err
}

// PeakInFlight returns the highest number of concurrent in-flight keys observed.
func (s *Shield) PeakInFlight() int64 {
	return s.peakInFlight.Load()
}

// InFlightCount returns the current number of in-flight origin fetches.
func (s *Shield) InFlightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// waitDone returns a channel that closes when the call's WaitGroup is done.
func (s *Shield) waitDone(c *call) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(ch)
	}()
	return ch
}

// Sentinel errors.
var (
	ErrTooManyWaiters = &shieldError{msg: "too many requests waiting for origin fetch"}
)

type shieldError struct {
	msg string
}

func (e *shieldError) Error() string {
	return e.msg
}
