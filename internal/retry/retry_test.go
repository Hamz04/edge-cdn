package retry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var (
	testRetryer     *Retryer
	testRetryerOnce sync.Once
)

func getTestRetryer() *Retryer {
	testRetryerOnce.Do(func() {
		testRetryer = New(Config{
			Name:           "test-shared",
			MaxAttempts:    3,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     10 * time.Millisecond,
			Multiplier:     2.0,
		})
	})
	return testRetryer
}

func TestDoSuccess(t *testing.T) {
	r := getTestRetryer()
	result, err := r.Do(context.Background(), func(ctx context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %v", result)
	}
}

func TestDoRetriesOnFailure(t *testing.T) {
	r := getTestRetryer()
	attempts := 0
	result, err := r.Do(context.Background(), func(ctx context.Context) (interface{}, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("transient")
		}
		return "success", nil
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if result != "success" {
		t.Fatalf("expected success, got %v", result)
	}
}

func TestDoExhaustsRetries(t *testing.T) {
	r := getTestRetryer()
	_, err := r.Do(context.Background(), func(ctx context.Context) (interface{}, error) {
		return nil, errors.New("always fails")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestDoRespectsContext(t *testing.T) {
	r := getTestRetryer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := r.Do(ctx, func(ctx context.Context) (interface{}, error) {
		return nil, errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("svc")
	if cfg.Name != "svc" {
		t.Fatalf("expected name svc, got %s", cfg.Name)
	}
	if cfg.MaxAttempts <= 0 {
		t.Fatal("default MaxAttempts should be positive")
	}
}
