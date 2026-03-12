package region

import (
	"sync"
	"testing"
)

func TestNewRouter(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("nil router")
	}
}

func TestAddAndList(t *testing.T) {
	r := New()
	r.AddNode("us-east", "10.0.1.1:8080")
	r.AddNode("us-east", "10.0.1.2:8080")
	r.AddNode("eu-west", "10.0.2.1:8080")
	regions := r.GetAllRegions()
	if len(regions) < 2 {
		t.Fatalf("want >=2 regions, got %d", len(regions))
	}
}

func TestGetNearest(t *testing.T) {
	r := New()
	r.AddNode("us-east", "10.0.1.1:8080")
	r.AddNode("eu-west", "10.0.2.1:8080")
	got := r.GetNearestRegion("us-east")
	if got == "" {
		t.Fatal("empty region")
	}
}

func TestFallbackUnknown(t *testing.T) {
	r := New()
	r.AddNode("us-east", "10.0.1.1:8080")
	got := r.GetNearestRegion("nonexistent")
	if got == "" {
		t.Fatal("should fallback")
	}
}

func TestSetHealth(t *testing.T) {
	r := New()
	r.AddNode("us-east", "10.0.1.1:8080")
	r.AddNode("eu-west", "10.0.2.1:8080")
	r.SetRegionHealth("us-east", false)
	got := r.GetNearestRegion("us-east")
	if got == "" {
		t.Fatal("should fallback when unhealthy")
	}
}

func TestConcurrentRegion(t *testing.T) {
	r := New()
	r.AddNode("us-east", "10.0.1.1:8080")
	r.AddNode("eu-west", "10.0.2.1:8080")
	r.AddNode("ap-south", "10.0.3.1:8080")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.GetNearestRegion("us-east") == "" {
				t.Errorf("empty")
			}
		}()
	}
	wg.Wait()
}
