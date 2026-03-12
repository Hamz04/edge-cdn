package retry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func newRetryer(max int) *Retryer {
	return New(Config{
		Name:           "test",
		MaxAttempts:    max,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     30 * time.Millisecond,
		Multiplier:     2.0,
	})
}

func TestFirstAttemptSuccess(t *testing.T) {
	r := newRetryer(3)
	n := 0
	result, err := r.Do(context.Background(), func(_ context.Context) (interface{}, error) {
		n++
		return "done", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "done" {
		t.Fatalf("want done, got %v", result)
	}
	if n != 1 {
		t.Fatalf("want 1 attempt, got %d", n)
	}
}

func TestRetriesUntilSuccess(t *testing.T) {
	r := newRetryer(5)
	n := 0
	_, err := r.Do(context.Background(), func(_ context.Context) (interface{}, error) {
		n++
		if n < 3 {
			return nil, errors.New("nope")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("want success, got %v", err)
	}
	if n != 3 {
		t.Fatalf("want 3 attempts, got %d", n)
	}
}

func TestExhausted(t *testing.T) {
	r := newRetryer(2)
	_, err := r.Do(context.Background(), func(_ context.Context) (interface{}, error) {
		return nil, errors.New("fail")
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !IsRetriesExhausted(err) {
		t.Fatalf("want RetriesExhaustedError, got %T", err)
	}
}

func TestContextCancel(t *testing.T) {
	r := newRetryer(100)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := r.Do(ctx, func(_ context.Context) (interface{}, error) {
		return nil, errors.New("fail")
	})
	if err == nil {
		t.Fatal("want error from cancelled ctx")
	}
}

func TestConcurrentRetries(t *testing.T) {
	r := newRetryer(3)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Do(context.Background(), func(_ context.Context) (interface{}, error) {
				return "ok", nil
			})
		}()
	}
	wg.Wait()
}
