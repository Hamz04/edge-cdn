package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoSuccess(t *testing.T) {
	r := New(Config{
		Name:           "test",
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		Multiplier:     2.0,
	})
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
	attempts := 0
	r := New(Config{
		Name:           "test",
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Multiplier:     1.5,
	})
	result, err := r.Do(context.Background(), func(ctx context.Context) (interface{}, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("transient")
		}
		return "success", nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if result != "success" {
		t.Fatalf("expected success, got %v", result)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestDoExhaustsRetries(t *testing.T) {
	r := New(Config{
		Name:           "test",
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Multiplier:     1.0,
	})
	_, err := r.Do(context.Background(), func(ctx context.Context) (interface{}, error) {
		return nil, errors.New("always fails")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestDoRespectsContext(t *testing.T) {
	r := New(Config{
		Name:           "test",
		MaxAttempts:    100,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		Multiplier:     1.0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
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
