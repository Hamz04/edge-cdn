package shield

import (
	"context"
	"sync"
	"testing"

	"github.com/Hamz04/edge-cdn/internal/origin"
)

var (
	testShield     *Shield
	testOrigin     *origin.Server
	testShieldOnce sync.Once
)

func getTestShield() (*Shield, *origin.Server) {
	testShieldOnce.Do(func() {
		testOrigin = origin.NewServer(1, 5)
		testShield = New(testOrigin, DefaultConfig())
	})
	return testShield, testOrigin
}

func TestNewShield(t *testing.T) {
	s, _ := getTestShield()
	if s == nil {
		t.Fatal("expected non-nil shield")
	}
}

func TestFetchSingle(t *testing.T) {
	s, _ := getTestShield()
	resp, _, err := s.Fetch(context.Background(), "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestFetchCoalescing(t *testing.T) {
	s, o := getTestShield()
	before := o.RequestCount()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Fetch(context.Background(), "/coalesce-unique-key")
		}()
	}
	wg.Wait()
	after := o.RequestCount()
	t.Logf("origin requests for 5 concurrent fetches: %d", after-before)
}
