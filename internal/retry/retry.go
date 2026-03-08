package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Config controls retry behavior.
type Config struct {
	// MaxAttempts is the total number of attempts (including the first).
	MaxAttempts int
	// InitialBackoff is the delay before the first retry.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential growth of the delay.
	MaxBackoff time.Duration
	// Multiplier scales the backoff between retries (typically 2.0).
	Multiplier float64
	// JitterFraction adds randomness to prevent thundering herds.
	// 0.0 = no jitter, 1.0 = up to 100% of the backoff added as random delay.
	JitterFraction float64
	// RetryableCheck is an optional function that decides whether an error
	// is worth retrying. If nil, all errors are retried.
	RetryableCheck func(error) bool
	// Name identifies this retryer in metrics.
	Name string
}

// DefaultConfig returns a production-ready config: 3 attempts, 100ms initial
// backoff, 5s max, 2x multiplier, 25% jitter.
func DefaultConfig(name string) Config {
	return Config{
		MaxAttempts:    3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		Multiplier:     2.0,
		JitterFraction: 0.25,
		Name:           name,
	}
}

// Retryer wraps unreliable operations with exponential backoff and jitter.
// Each retry is visible in Prometheus metrics so you can alert on retry storms.
//
// Usage:
//
//	r := retry.New(retry.DefaultConfig("origin-fetch"))
//	result, err := r.Do(ctx, func(ctx context.Context) (interface{}, error) {
//	    return origin.Fetch(path)
//	})
type Retryer struct {
	cfg Config
	rng *rand.Rand

	attemptsTotal *prometheus.CounterVec
	retriesTotal  prometheus.Counter
	exhaustedTotal prometheus.Counter
	retryLatency  prometheus.Histogram
}

// New creates a Retryer with the given config.
func New(cfg Config) *Retryer {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 5 * time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}
	if cfg.Name == "" {
		cfg.Name = "default"
	}

	return &Retryer{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		attemptsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "retry_attempts_total",
			Help:        "Total retry attempts by outcome.",
			ConstLabels: prometheus.Labels{"retryer": cfg.Name},
		}, []string{"outcome"}),
		retriesTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "retry_retries_total",
			Help:        "Total retried operations (not counting first attempt).",
			ConstLabels: prometheus.Labels{"retryer": cfg.Name},
		}),
		exhaustedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "retry_exhausted_total",
			Help:        "Times all retry attempts were exhausted.",
			ConstLabels: prometheus.Labels{"retryer": cfg.Name},
		}),
		retryLatency: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace:   "edgecdn",
			Name:        "retry_total_duration_seconds",
			Help:        "Total time spent across all attempts including backoff.",
			Buckets:     []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			ConstLabels: prometheus.Labels{"retryer": cfg.Name},
		}),
	}
}

// Do executes fn with retries. The context is checked between attempts;
// if it's cancelled or times out, Do returns immediately.
func (r *Retryer) Do(ctx context.Context, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	start := time.Now()
	var lastErr error

	for attempt := 0; attempt < r.cfg.MaxAttempts; attempt++ {
		// Check context before each attempt.
		if err := ctx.Err(); err != nil {
			r.attemptsTotal.WithLabelValues("context_cancelled").Inc()
			r.retryLatency.Observe(time.Since(start).Seconds())
			return nil, err
		}

		result, err := fn(ctx)
		if err == nil {
			r.attemptsTotal.WithLabelValues("success").Inc()
			r.retryLatency.Observe(time.Since(start).Seconds())
			return result, nil
		}

		lastErr = err
		r.attemptsTotal.WithLabelValues("failure").Inc()

		// Check if this error is retryable.
		if r.cfg.RetryableCheck != nil && !r.cfg.RetryableCheck(err) {
			r.retryLatency.Observe(time.Since(start).Seconds())
			return nil, err
		}

		// Don't sleep after the last attempt.
		if attempt < r.cfg.MaxAttempts-1 {
			backoff := r.calculateBackoff(attempt)
			r.retriesTotal.Inc()

			select {
			case <-time.After(backoff):
				// Continue to next attempt.
			case <-ctx.Done():
				r.attemptsTotal.WithLabelValues("context_cancelled").Inc()
				r.retryLatency.Observe(time.Since(start).Seconds())
				return nil, ctx.Err()
			}
		}
	}

	r.exhaustedTotal.Inc()
	r.retryLatency.Observe(time.Since(start).Seconds())
	return nil, &RetriesExhaustedError{Attempts: r.cfg.MaxAttempts, Last: lastErr}
}

// calculateBackoff computes the delay for a given attempt using exponential
// backoff with jitter. attempt is 0-indexed (0 = after first failure).
func (r *Retryer) calculateBackoff(attempt int) time.Duration {
	// Exponential: initialBackoff * multiplier^attempt
	backoffFloat := float64(r.cfg.InitialBackoff) * math.Pow(r.cfg.Multiplier, float64(attempt))

	// Cap at max.
	if backoffFloat > float64(r.cfg.MaxBackoff) {
		backoffFloat = float64(r.cfg.MaxBackoff)
	}

	// Add jitter: backoff + random(0, backoff * jitterFraction).
	if r.cfg.JitterFraction > 0 {
		jitter := backoffFloat * r.cfg.JitterFraction * r.rng.Float64()
		backoffFloat += jitter
	}

	return time.Duration(backoffFloat)
}

// RetriesExhaustedError is returned when all retry attempts fail.
type RetriesExhaustedError struct {
	Attempts int
	Last     error
}

func (e *RetriesExhaustedError) Error() string {
	if e.Last != nil {
		return "all " + itoa(e.Attempts) + " retry attempts exhausted, last error: " + e.Last.Error()
	}
	return "all " + itoa(e.Attempts) + " retry attempts exhausted"
}

func (e *RetriesExhaustedError) Unwrap() error {
	return e.Last
}

// IsRetriesExhausted checks if an error is a RetriesExhaustedError.
func IsRetriesExhausted(err error) bool {
	var target *RetriesExhaustedError
	return errors.As(err, &target)
}

func itoa(n int) string {
	if n < 0 {
		return "-" + uitoa(uint(-n))
	}
	return uitoa(uint(n))
}

func uitoa(n uint) string {
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
