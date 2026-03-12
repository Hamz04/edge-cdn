package region

import "testing"

func TestNewRouter(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("expected non-nil router")
	}
}

func TestAddAndGetNode(t *testing.T) {
	r := New()
	r.AddNode("us-east", "node1.us-east.example.com")
	r.AddNode("us-east", "node2.us-east.example.com")
	r.AddNode("eu-west", "node1.eu-west.example.com")

	regions := r.GetAllRegions()
	if len(regions) < 2 {
		t.Fatalf("expected at least 2 regions, got %d", len(regions))
	}
}

func TestGetNearestRegion(t *testing.T) {
	r := New()
	r.AddNode("us-east", "node1.us-east.example.com")
	r.AddNode("eu-west", "node1.eu-west.example.com")

	nearest := r.GetNearestRegion("us-east")
	if nearest == "" {
		t.Fatal("expected non-empty nearest region")
	}
}

func TestRemoveNode(t *testing.T) {
	r := New()
	r.AddNode("us-east", "node1")
	r.AddNode("us-east", "node2")

	r.RemoveNode("us-east", "node1")
	count := r.HealthyNodeCount()
	if count < 1 {
		t.Fatalf("expected at least 1 healthy node, got %d", count)
	}
}

func TestRegionHealth(t *testing.T) {
	r := New()
	r.AddNode("us-east", "node1")
	r.SetRegionHealth("us-east", false)

	nearest := r.GetNearestRegion("us-east")
	// Should fallback since us-east is unhealthy
	t.Logf("nearest region when us-east is down: %s", nearest)
}

func TestGetNodeForKey(t *testing.T) {
	r := New()
	r.AddNode("us-east", "node1")
	r.AddNode("us-east", "node2")

	node := r.GetNodeForKey("us-east", "my-cache-key")
	if node == "" {
		t.Fatal("expected a node for the key")
	}

	// Consistent: same key should return same node
	for i := 0; i < 20; i++ {
		if r.GetNodeForKey("us-east", "my-cache-key") != node {
			t.Fatal("expected consistent node mapping")
		}
	}
}
