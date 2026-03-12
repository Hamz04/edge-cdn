package hashing

import "testing"

func TestNewRing(t *testing.T) {
	r := NewRing(100)
	if r == nil {
		t.Fatal("expected non-nil ring")
	}
	if r.NodeCount() != 0 {
		t.Fatalf("expected 0 nodes, got %d", r.NodeCount())
	}
}

func TestAddRemoveNode(t *testing.T) {
	r := NewRing(50)
	if !r.AddNode("node1") {
		t.Fatal("expected AddNode to return true")
	}
	if r.NodeCount() != 1 {
		t.Fatalf("expected 1 node, got %d", r.NodeCount())
	}
	// duplicate add
	if r.AddNode("node1") {
		t.Fatal("expected false for duplicate add")
	}
	if !r.RemoveNode("node1") {
		t.Fatal("expected RemoveNode to return true")
	}
	if r.NodeCount() != 0 {
		t.Fatalf("expected 0 nodes after remove, got %d", r.NodeCount())
	}
}

func TestGetNodeConsistency(t *testing.T) {
	r := NewRing(100)
	r.AddNode("node-a")
	r.AddNode("node-b")
	r.AddNode("node-c")

	key := "test-key-123"
	first := r.GetNode(key)
	for i := 0; i < 50; i++ {
		if r.GetNode(key) != first {
			t.Fatal("consistent hashing returned different node for same key")
		}
	}
}

func TestGetNodeEmptyRing(t *testing.T) {
	r := NewRing(50)
	node := r.GetNode("any-key")
	if node != "" {
		t.Fatalf("expected empty string for empty ring, got %q", node)
	}
}

func TestGetNodes(t *testing.T) {
	r := NewRing(50)
	r.AddNode("a")
	r.AddNode("b")
	nodes := r.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}
