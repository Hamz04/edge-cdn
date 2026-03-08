package circuitbreaker

import (
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type State int

const (
	StateClosed   State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

var (
	ErrCircuitOpen = errors.New("circuit breaker is open")
)

type Config struct {
	FailThreshold         int
	SuccessThreshold      int
	OpenTimeout           time.Duration
	HalfOpenMaxConcurrent int
	Name                  string
}

func DefaultConfig(name string) Config {
	return Config{
		FailThreshold:         5,
		SuccessThreshold:      2,
		OpenTimeout:           30 * time.Second,
		HalfOpenMaxConcurrent: 1,
		Name:                  name,
	}
}

type Breaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	successes        int
	lastStateChange  time.Time
	cfg              Config
	halfOpenInFlight int

	stateGauge    prometheus.Gauge
	tripsTotal    prometheus.Counter
	successTotal  prometheus.Counter
	failureTotal  prometheus.Counter
	rejectedTotal prometheus.Counter
}

func New(cfg Config) *Breaker {
	if cfg.FailThreshold <= 0 {
		cfg.FailThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 2
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxConcurrent <= 0 {
		cfg.HalfOpenMaxConcurrent = 1
	}
	if cfg.Name == "" {
		cfg.Name = "default"
	}

	return &Breaker{
		state:           StateClosed,
		lastStateChange: time.Now(),
		cfg:             cfg,
		stateGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace:   "edgecdn",
			Name:        "circuit_breaker_state",
			Help:        "Current circuit breaker state (0=closed, 1=open, 2=half-open).",
			ConstLabels: prometheus.Labels{"breaker": cfg.Name},
		}),
		tripsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "circuit_breaker_trips_total",
			Help:        "Total times the circuit breaker tripped open.",
			ConstLabels: prometheus.Labels{"breaker": cfg.Name},
		}),
		successTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "circuit_breaker_success_total",
			Help:        "Total successful executions.",
			ConstLabels: prometheus.Labels{"breaker": cfg.Name},
		}),
		failureTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "circuit_breaker_failure_total",
			Help:        "Total failed executions.",
			ConstLabels: prometheus.Labels{"breaker": cfg.Name},
		}),
		rejectedTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace:   "edgecdn",
			Name:        "circuit_breaker_rejected_total",
			Help:        "Total requests rejected by open circuit.",
			ConstLabels: prometheus.Labels{"breaker": cfg.Name},
		}),
	}
}

func (b *Breaker) Execute(fn func() (interface{}, error)) (interface{}, error) {
	b.mu.Lock()
	switch b.state {
	case StateOpen:
		if time.Since(b.lastStateChange) > b.cfg.OpenTimeout {
			b.setState(StateHalfOpen)
		} else {
			b.mu.Unlock()
			b.rejectedTotal.Inc()
			return nil, ErrCircuitOpen
		}
	case StateHalfOpen:
		if b.halfOpenInFlight >= b.cfg.HalfOpenMaxConcurrent {
			b.mu.Unlock()
			b.rejectedTotal.Inc()
			return nil, ErrCircuitOpen
		}
	}
	if b.state == StateHalfOpen {
		b.halfOpenInFlight++
	}
	b.mu.Unlock()

	result, err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateHalfOpen {
		b.halfOpenInFlight--
	}
	if err != nil {
		b.onFailure()
		return result, err
	}
	b.onSuccess()
	return result, nil
}

func (b *Breaker) onSuccess() {
	b.successTotal.Inc()
	switch b.state {
	case StateClosed:
		b.failures = 0
	case StateHalfOpen:
		b.successes++
		if b.successes >= b.cfg.SuccessThreshold {
			b.setState(StateClosed)
		}
	}
}

func (b *Breaker) onFailure() {
	b.failureTotal.Inc()
	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.cfg.FailThreshold {
			b.setState(StateOpen)
			b.tripsTotal.Inc()
		}
	case StateHalfOpen:
		b.setState(StateOpen)
		b.tripsTotal.Inc()
	}
}

func (b *Breaker) setState(newState State) {
	b.state = newState
	b.lastStateChange = time.Now()
	b.failures = 0
	b.successes = 0
	b.halfOpenInFlight = 0
	b.stateGauge.Set(float64(newState))
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen && time.Since(b.lastStateChange) > b.cfg.OpenTimeout {
		b.setState(StateHalfOpen)
	}
	return b.state
}

func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setState(StateClosed)
}
