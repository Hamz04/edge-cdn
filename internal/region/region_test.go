package region

import (
	"sync"
	"testing"
)

var (
	testRouter     *Router
	testRouterOnce sync.Once
)

func getTestRouter() *Router {
	testRouterOnce.Do(func() {
		testRouter = New()
	})
	return testRouter
}

func TestNewRouter(t *testing.T) {
	r := getTestRouter()
	if r == nil {
		t.Fatal("expected non-nil router")
	}
}

func TestAddAndGetNode(t *testing.T) {
	r := getTestRouter()
	r.AddNode("us-east", "node1.us-east.example.com")
	r.AddNode("us-east", "node2.us-east.example.com")
	r.AddNode("eu-west", "node1.eu-west.example.com")
	regions := r.GetAllRegions()
	if len(regions) < 2 {
		t.Fatalf("expected at least 2 regions, got %d", len(regions))
	}
}

func TestGetNearestRegion(t *testing.T) {
	r := getTestRouter()
	nearest := r.GetNearestRegion("us-east")
	if nearest == "" {
		t.Fatal("expected non-empty nearest region")
	}
}

func TestGetNodeForKey(t *testing.T) {
	r := getTestRouter()
	node := r.GetNodeForKey("us-east", "my-cache-key")
	if node == "" {
		t.Fatal("expected a node for the key")
	}
	for i := 0; i < 20; i++ {
		if r.GetNodeForKey("us-east", "my-cache-key") != node {
			t.Fatal("expected consistent node mapping")
		}
	}
}

func TestRegionHealth(t *testing.T) {
	r := getTestRouter()
	r.SetRegionHealth("us-east", false)
	nearest := r.GetNearestRegion("us-east")
	t.Logf("nearest when us-east down: %s", nearest)
	r.SetRegionHealth("us-east", true) // restore
}

func TestHealthyNodeCount(t *testing.T) {
	r := getTestRouter()
	count := r.HealthyNodeCount()
	if count < 1 {
		t.Fatalf("expected at least 1 healthy node, got %d", count)
	}
}
