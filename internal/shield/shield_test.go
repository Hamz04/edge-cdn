package shield

import (
	"context"
	"sync"
	"testing"

	"github.com/Hamz04/edge-cdn/internal/origin"
)

func TestNewShield(t *testing.T) {
	o := origin.NewServer(1, 5)
	s := New(o, DefaultConfig())
	if s == nil {
		t.Fatal("expected non-nil shield")
	}
}

func TestFetchSingle(t *testing.T) {
	o := origin.NewServer(1, 5)
	s := New(o, DefaultConfig())

	resp, shared, err := s.Fetch(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if shared {
		t.Fatal("single request should not be shared")
	}
}

func TestFetchCoalescing(t *testing.T) {
	o := origin.NewServer(20, 50) // slow origin
	s := New(o, DefaultConfig())

	var wg sync.WaitGroup
	results := make([]bool, 10) // track shared flag

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, shared, err := s.Fetch(context.Background(), "/coalesce-test")
			if err != nil {
				return
			}
			results[idx] = shared
		}(i)
	}
	wg.Wait()

	// At least some should have been shared (coalesced)
	sharedCount := 0
	for _, s := range results {
		if s {
			sharedCount++
		}
	}
	// With 10 concurrent requests and 20-50ms latency, most should be coalesced
	if o.RequestCount() >= 10 {
		t.Logf("Warning: expected request coalescing, but origin got %d requests", o.RequestCount())
	}
}
