package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestNewBreaker(t *testing.T) {
	cfg := Config{
		Name:                "test",
		FailThreshold:      3,
		SuccessThreshold:   2,
		OpenTimeout:        50 * time.Millisecond,
		HalfOpenMaxConcurrent: 1,
	}
	b := New(cfg)
	if b == nil {
		t.Fatal("expected non-nil breaker")
	}
	if b.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %v", b.State())
	}
}

func TestExecuteSuccess(t *testing.T) {
	b := New(DefaultConfig("test"))
	result, err := b.Execute(func() (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %v", result)
	}
}

func TestTripsAfterFailThreshold(t *testing.T) {
	cfg := Config{
		Name:                "test",
		FailThreshold:      2,
		SuccessThreshold:   1,
		OpenTimeout:        100 * time.Millisecond,
		HalfOpenMaxConcurrent: 1,
	}
	b := New(cfg)
	testErr := errors.New("fail")

	for i := 0; i < 2; i++ {
		b.Execute(func() (interface{}, error) { return nil, testErr })
	}

	if b.State() != StateOpen {
		t.Fatalf("expected StateOpen after failures, got %v", b.State())
	}

	_, err := b.Execute(func() (interface{}, error) { return "ok", nil })
	if err == nil {
		t.Fatal("expected error when circuit is open")
	}
}

func TestResetRestoresClosed(t *testing.T) {
	cfg := Config{
		Name:                "test",
		FailThreshold:      1,
		SuccessThreshold:   1,
		OpenTimeout:        time.Second,
		HalfOpenMaxConcurrent: 1,
	}
	b := New(cfg)
	b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	if b.State() != StateOpen {
		t.Fatalf("expected open, got %v", b.State())
	}
	b.Reset()
	if b.State() != StateClosed {
		t.Fatalf("expected closed after reset, got %v", b.State())
	}
}

func TestHalfOpenTransition(t *testing.T) {
	cfg := Config{
		Name:                "test",
		FailThreshold:      1,
		SuccessThreshold:   1,
		OpenTimeout:        30 * time.Millisecond,
		HalfOpenMaxConcurrent: 1,
	}
	b := New(cfg)
	b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	time.Sleep(50 * time.Millisecond)

	result, err := b.Execute(func() (interface{}, error) { return "recovered", nil })
	if err != nil {
		t.Fatalf("expected success in half-open, got error: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("expected recovered, got %v", result)
	}
	if b.State() != StateClosed {
		t.Fatalf("expected closed after success in half-open, got %v", b.State())
	}
}
