package hashing

import (
	"fmt"
	"testing"
)

func TestNewRing(t *testing.T) {
	r := NewRing(100)
	if r == nil {
		t.Fatal("nil ring")
	}
}

func TestConsistency(t *testing.T) {
	r := NewRing(100)
	r.AddNode("n1")
	r.AddNode("n2")
	r.AddNode("n3")
	first := r.GetNode("/foo/bar")
	for i := 0; i < 200; i++ {
		if got := r.GetNode("/foo/bar"); got != first {
			t.Fatalf("inconsistent: %s vs %s", first, got)
		}
	}
}

func TestDistribution(t *testing.T) {
	r := NewRing(150)
	nodes := []string{"a", "b", "c"}
	for _, n := range nodes {
		r.AddNode(n)
	}
	counts := map[string]int{}
	for i := 0; i < 9000; i++ {
		counts[r.GetNode(fmt.Sprintf("/k/%d", i))]++
	}
	for _, n := range nodes {
		if float64(counts[n])/9000 < 0.15 {
			t.Errorf("%s got only %d/9000", n, counts[n])
		}
	}
}

func TestEmptyRing(t *testing.T) {
	r := NewRing(100)
	if got := r.GetNode("/x"); got != "" {
		t.Fatalf("empty ring returned %s", got)
	}
}

func TestSingleNode(t *testing.T) {
	r := NewRing(100)
	r.AddNode("solo")
	for i := 0; i < 50; i++ {
		if got := r.GetNode(fmt.Sprintf("/%d", i)); got != "solo" {
			t.Fatalf("want solo, got %s", got)
		}
	}
}

func TestRemoveNode(t *testing.T) {
	r := NewRing(100)
	r.AddNode("a")
	r.AddNode("b")
	r.RemoveNode("a")
	if r.NodeCount() != 1 {
		t.Fatalf("want 1 node, got %d", r.NodeCount())
	}
}
