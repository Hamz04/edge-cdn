package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type tokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
}

func (tb *tokenBucket) allow() bool {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
	tb.lastRefill = now
	if tb.tokens >= 1.0 {
		tb.tokens--
		return true
	}
	return false
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     int
	burst   int
	stopCh  chan struct{}
	once    sync.Once

	allowedTotal prometheus.Counter
	blockedTotal prometheus.Counter
	activeIPs    prometheus.Gauge
}

func New(rps, burst int) *Limiter {
	if rps <= 0 {
		rps = 100
	}
	if burst <= 0 {
		burst = rps * 2
	}

	l := &Limiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		burst:   burst,
		stopCh:  make(chan struct{}),
		allowedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "ratelimit_allowed_total",
			Help:      "Total requests allowed by rate limiter.",
		}),
		blockedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: "edgecdn",
			Name:      "ratelimit_blocked_total",
			Help:      "Total requests blocked by rate limiter.",
		}),
		activeIPs: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: "edgecdn",
			Name:      "ratelimit_active_ips",
			Help:      "Number of IPs currently tracked by rate limiter.",
		}),
	}

	go l.cleanup()
	return l
}

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[key]
	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(l.burst),
			maxTokens:  float64(l.burst),
			refillRate: float64(l.rps),
			lastRefill: time.Now(),
		}
		l.buckets[key] = bucket
		l.activeIPs.Set(float64(len(l.buckets)))
	}

	if bucket.allow() {
		l.allowedTotal.Inc()
		return true
	}
	l.blockedTotal.Inc()
	return false
}

func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !l.Allow(ip) {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-RateLimit-Limit", itoa(l.rps))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) Stop() {
	l.once.Do(func() { close(l.stopCh) })
}

func (l *Limiter) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for key, bucket := range l.buckets {
				if now.Sub(bucket.lastRefill) > 5*time.Minute {
					delete(l.buckets, key)
				}
			}
			l.activeIPs.Set(float64(len(l.buckets)))
			l.mu.Unlock()
		case <-l.stopCh:
			return
		}
	}
}

func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
