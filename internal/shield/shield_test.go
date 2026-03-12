package shield

import (
	"context"
	"sync"
	"testing"

	"github.com/Hamz04/edge-cdn/internal/origin"
)

func TestFetchSingle(t *testing.T) {
	o := origin.NewServer(5, 20)
	s := New(o, DefaultConfig())
	resp, _, err := s.Fetch(context.Background(), "/test.html")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
}

func TestCoalescing(t *testing.T) {
	o := origin.NewServer(30, 80)
	s := New(o, DefaultConfig())
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _, err := s.Fetch(context.Background(), "/coalesce.html")
			errs[idx] = err
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("req %d: %v", i, e)
		}
	}
}

func TestDifferentPaths(t *testing.T) {
	o := origin.NewServer(5, 20)
	s := New(o, DefaultConfig())
	r1, _, e1 := s.Fetch(context.Background(), "/a.html")
	r2, _, e2 := s.Fetch(context.Background(), "/b.css")
	if e1 != nil || e2 != nil {
		t.Fatalf("errors: %v, %v", e1, e2)
	}
	if r1 == nil || r2 == nil {
		t.Fatal("nil responses")
	}
}
