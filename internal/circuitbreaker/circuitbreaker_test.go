package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func testBreaker(threshold int, timeout time.Duration) *Breaker {
	return New(Config{
		Name:                  "test-cb",
		FailThreshold:         threshold,
		SuccessThreshold:      1,
		OpenTimeout:           timeout,
		HalfOpenMaxConcurrent: 1,
	})
}

func TestClosed_SuccessStaysClosed(t *testing.T) {
	cb := testBreaker(3, time.Second)
	for i := 0; i < 10; i++ {
		_, err := cb.Execute(func() (interface{}, error) { return "ok", nil })
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if cb.State() != StateClosed {
		t.Fatalf("want closed, got %s", cb.State().String())
	}
}

func TestOpensAfterThreshold(t *testing.T) {
	cb := testBreaker(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		cb.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	}
	if cb.State() != StateOpen {
		t.Fatalf("want open, got %s", cb.State().String())
	}
	_, err := cb.Execute(func() (interface{}, error) { return "ok", nil })
	if err == nil {
		t.Fatal("open breaker should reject")
	}
}

func TestHalfOpenSuccess(t *testing.T) {
	cb := testBreaker(2, 40*time.Millisecond)
	for i := 0; i < 2; i++ {
		cb.Execute(func() (interface{}, error) { return nil, errors.New("x") })
	}
	time.Sleep(50 * time.Millisecond)
	_, err := cb.Execute(func() (interface{}, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("half-open should allow: %v", err)
	}
	if cb.State() != StateClosed {
		t.Fatalf("want closed after half-open success, got %s", cb.State().String())
	}
}

func TestHalfOpenFailReopens(t *testing.T) {
	cb := testBreaker(2, 40*time.Millisecond)
	for i := 0; i < 2; i++ {
		cb.Execute(func() (interface{}, error) { return nil, errors.New("x") })
	}
	time.Sleep(50 * time.Millisecond)
	cb.Execute(func() (interface{}, error) { return nil, errors.New("still broken") })
	if cb.State() != StateOpen {
		t.Fatalf("want open after half-open fail, got %s", cb.State().String())
	}
}

func TestReset(t *testing.T) {
	cb := testBreaker(2, time.Second)
	for i := 0; i < 2; i++ {
		cb.Execute(func() (interface{}, error) { return nil, errors.New("x") })
	}
	cb.Reset()
	if cb.State() != StateClosed {
		t.Fatalf("want closed after reset, got %s", cb.State().String())
	}
}

func TestConcurrent(t *testing.T) {
	cb := testBreaker(1000, time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.Execute(func() (interface{}, error) { return "ok", nil })
		}()
	}
	wg.Wait()
}
