package region

import (
	"math"
	"sync"

	"github.com/Hamz04/edge-cdn/internal/hashing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Region represents a geographic edge location with its own pool of cache nodes.
// Each region maintains an independent consistent hash ring so traffic within
// a region stays local — reducing cross-region hops and keeping latency low.
type Region struct {
	Name    string   `json:"name"`
	Lat     float64  `json:"lat"`
	Lng     float64  `json:"lng"`
	Nodes   []string `json:"nodes"`
	Healthy bool     `json:"healthy"`
	ring    *hashing.Ring
}

// Router handles geographic request routing across edge regions.
// It maps incoming requests to the nearest healthy region, then uses
// consistent hashing within that region to pick a specific node.
type Router struct {
	mu      sync.RWMutex
	regions map[string]*Region
	order   []string // round-robin index source
	rrIndex int      // current round-robin position

	// Prometheus
	requestsByRegion *prometheus.CounterVec
	regionHealth     *prometheus.GaugeVec
}

// predefined regions with realistic coordinates
var defaultRegions = map[string][2]float64{
	"us-east":    {39.0438, -77.4874},  // Virginia
	"us-west":    {45.5945, -122.1562}, // Oregon
	"eu-west":    {53.3331, -6.2489},   // Ireland
	"eu-central": {50.1109, 8.6821},    // Frankfurt
	"ap-south":   {19.0760, 72.8777},   // Mumbai
}

func New() *Router {
	r := &Router{
		regions: make(map[string]*Region),
		order:   make([]string, 0, len(defaultRegions)),
		requestsByRegion: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "edgecdn",
				Name:      "requests_by_region_total",
				Help:      "Total requests routed to each region.",
			},
			[]string{"region"},
		),
		regionHealth: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "edgecdn",
				Name:      "region_health",
				Help:      "Health status of each region (1=healthy, 0=unhealthy).",
			},
			[]string{"region"},
		),
	}

	// Initialize all predefined regions with empty node pools.
	for name, coords := range defaultRegions {
		r.regions[name] = &Region{
			Name:    name,
			Lat:     coords[0],
			Lng:     coords[1],
			Nodes:   make([]string, 0),
			Healthy: true,
			ring:    hashing.NewRing(150),
		}
		r.order = append(r.order, name)
		r.regionHealth.WithLabelValues(name).Set(1)
	}

	return r
}

// AddNode registers a cache node in the specified region.
// Creates the region if it doesn't exist (supports custom regions beyond the 5 defaults).
func (r *Router) AddNode(regionName, nodeAddr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, exists := r.regions[regionName]
	if !exists {
		reg = &Region{
			Name:    regionName,
			Healthy: true,
			ring:    hashing.NewRing(150),
		}
		r.regions[regionName] = reg
		r.order = append(r.order, regionName)
		r.regionHealth.WithLabelValues(regionName).Set(1)
	}

	// Avoid duplicate registration.
	for _, n := range reg.Nodes {
		if n == nodeAddr {
			return
		}
	}

	reg.Nodes = append(reg.Nodes, nodeAddr)
	reg.ring.AddNode(nodeAddr)
}

// RemoveNode unregisters a cache node from the specified region.
// If the region has no remaining nodes, it's marked unhealthy.
func (r *Router) RemoveNode(regionName, nodeAddr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, exists := r.regions[regionName]
	if !exists {
		return
	}

	reg.ring.RemoveNode(nodeAddr)

	filtered := make([]string, 0, len(reg.Nodes))
	for _, n := range reg.Nodes {
		if n != nodeAddr {
			filtered = append(filtered, n)
		}
	}
	reg.Nodes = filtered

	if len(reg.Nodes) == 0 {
		reg.Healthy = false
		r.regionHealth.WithLabelValues(regionName).Set(0)
	}
}

// GetNearestRegion returns the closest healthy region to the given client region.
// If clientRegion is empty or unknown, falls back to round-robin across all healthy regions.
func (r *Router) GetNearestRegion(clientRegion string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Direct match — if the requested region is healthy and has nodes, use it.
	if reg, ok := r.regions[clientRegion]; ok && reg.Healthy && len(reg.Nodes) > 0 {
		r.requestsByRegion.WithLabelValues(clientRegion).Inc()
		return clientRegion
	}

	// If client specified a known region that's unhealthy, find the geographically closest healthy one.
	if src, ok := r.regions[clientRegion]; ok {
		bestDist := math.MaxFloat64
		bestRegion := ""
		for name, reg := range r.regions {
			if !reg.Healthy || len(reg.Nodes) == 0 || name == clientRegion {
				continue
			}
			d := haversine(src.Lat, src.Lng, reg.Lat, reg.Lng)
			if d < bestDist {
				bestDist = d
				bestRegion = name
			}
		}
		if bestRegion != "" {
			r.requestsByRegion.WithLabelValues(bestRegion).Inc()
			return bestRegion
		}
	}

	// No region hint or nothing matched — round-robin across healthy regions.
	return r.roundRobin()
}

// GetNodeForKey uses consistent hashing within a region to pick the node for a cache key.
func (r *Router) GetNodeForKey(regionName, key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, ok := r.regions[regionName]
	if !ok || len(reg.Nodes) == 0 {
		return ""
	}
	return reg.ring.GetNode(key)
}

// SetRegionHealth overrides the health status of a region.
func (r *Router) SetRegionHealth(regionName string, healthy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, ok := r.regions[regionName]
	if !ok {
		return
	}
	reg.Healthy = healthy
	val := 0.0
	if healthy {
		val = 1.0
	}
	r.regionHealth.WithLabelValues(regionName).Set(val)
}

// GetAllRegions returns a snapshot of every region and its current state.
func (r *Router) GetAllRegions() []Region {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Region, 0, len(r.regions))
	for _, reg := range r.regions {
		snapshot := Region{
			Name:    reg.Name,
			Lat:     reg.Lat,
			Lng:     reg.Lng,
			Nodes:   make([]string, len(reg.Nodes)),
			Healthy: reg.Healthy,
		}
		copy(snapshot.Nodes, reg.Nodes)
		out = append(out, snapshot)
	}
	return out
}

// HealthyNodeCount returns how many nodes are available across all healthy regions.
func (r *Router) HealthyNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, reg := range r.regions {
		if reg.Healthy {
			count += len(reg.Nodes)
		}
	}
	return count
}

// roundRobin picks the next healthy region in order (caller must hold at least RLock).
func (r *Router) roundRobin() string {
	if len(r.order) == 0 {
		return ""
	}

	// Try each region starting from current index.
	for i := 0; i < len(r.order); i++ {
		idx := (r.rrIndex + i) % len(r.order)
		name := r.order[idx]
		if reg, ok := r.regions[name]; ok && reg.Healthy && len(reg.Nodes) > 0 {
			r.rrIndex = (idx + 1) % len(r.order)
			r.requestsByRegion.WithLabelValues(name).Inc()
			return name
		}
	}

	// Everything is down — return first region as last resort.
	return r.order[0]
}

// haversine calculates the great-circle distance in km between two lat/lng points.
// Used for finding the geographically closest region during failover.
func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKm = 6371.0

	dLat := degreesToRadians(lat2 - lat1)
	dLng := degreesToRadians(lng2 - lng1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLng/2)*math.Sin(dLng/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180.0
}

// RegionInfo holds public region metadata for the Stage 3 gateway.
type RegionInfo struct {
	Name      string
	Latitude  float64
	Longitude float64
}

// DefaultRegions returns a slice of all predefined regions with coordinates.
func DefaultRegions() []RegionInfo {
	result := make([]RegionInfo, 0, len(defaultRegions))
	for name, coords := range defaultRegions {
		result = append(result, RegionInfo{
			Name:      name,
			Latitude:  coords[0],
			Longitude: coords[1],
		})
	}
	return result
}
