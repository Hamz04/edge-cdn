package origin

import (
	"testing"
)

func TestNewServer(t *testing.T) {
	s := NewServer(10, 50)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestFetch(t *testing.T) {
	s := NewServer(1, 5)
	resp, err := s.Fetch("/test-path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(resp.Body) == 0 {
		t.Fatal("expected non-empty body")
	}
}

func TestRequestCount(t *testing.T) {
	s := NewServer(1, 2)
	initial := s.RequestCount()
	s.Fetch("/a")
	s.Fetch("/b")
	if s.RequestCount() != initial+2 {
		t.Fatalf("expected count %d, got %d", initial+2, s.RequestCount())
	}
}
