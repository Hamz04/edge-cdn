package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var (
	testBreaker     *Breaker
	testBreakerOnce sync.Once
)

func getTestBreaker() *Breaker {
	testBreakerOnce.Do(func() {
		testBreaker = New(Config{
			Name:                  "test-shared",
			FailThreshold:        3,
			SuccessThreshold:     2,
			OpenTimeout:          50 * time.Millisecond,
			HalfOpenMaxConcurrent: 1,
		})
	})
	return testBreaker
}

func TestNewBreaker(t *testing.T) {
	b := getTestBreaker()
	if b == nil {
		t.Fatal("expected non-nil breaker")
	}
}

func TestExecuteSuccess(t *testing.T) {
	b := getTestBreaker()
	b.Reset()
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
	b := getTestBreaker()
	b.Reset()
	testErr := errors.New("fail")
	for i := 0; i < 3; i++ {
		b.Execute(func() (interface{}, error) { return nil, testErr })
	}
	if b.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", b.State())
	}
}

func TestResetRestoresClosed(t *testing.T) {
	b := getTestBreaker()
	b.Reset()
	b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	b.Reset()
	if b.State() != StateClosed {
		t.Fatalf("expected closed after reset, got %v", b.State())
	}
}

func TestHalfOpenTransition(t *testing.T) {
	b := getTestBreaker()
	b.Reset()
	for i := 0; i < 3; i++ {
		b.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	}
	time.Sleep(60 * time.Millisecond)
	result, err := b.Execute(func() (interface{}, error) { return "recovered", nil })
	if err != nil {
		t.Fatalf("expected success in half-open: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("expected recovered, got %v", result)
	}
}
