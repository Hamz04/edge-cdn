package hashing

import (
	"fmt"
	"sort"
	"sync"

	"github.com/cespare/xxhash/v2"
)

const defaultVNodes = 150

type Ring struct {
	mu      sync.RWMutex
	vnodes  int
	nodes   map[string]bool
	ring    []uint64
	ringMap map[uint64]string
}

func NewRing(vnodes int) *Ring {
	if vnodes <= 0 {
		vnodes = defaultVNodes
	}
	return &Ring{
		vnodes:  vnodes,
		nodes:   make(map[string]bool),
		ring:    make([]uint64, 0),
		ringMap: make(map[uint64]string),
	}
}

func (r *Ring) AddNode(node string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodes[node] {
		return false
	}
	r.nodes[node] = true
	for i := 0; i < r.vnodes; i++ {
		h := hashKey(fmt.Sprintf("%s#%d", node, i))
		r.ring = append(r.ring, h)
		r.ringMap[h] = node
	}
	sort.Slice(r.ring, func(i, j int) bool { return r.ring[i] < r.ring[j] })
	return true
}

func (r *Ring) RemoveNode(node string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.nodes[node] {
		return false
	}
	delete(r.nodes, node)
	newRing := make([]uint64, 0, len(r.ring)-r.vnodes)
	for _, h := range r.ring {
		if r.ringMap[h] != node {
			newRing = append(newRing, h)
		} else {
			delete(r.ringMap, h)
		}
	}
	r.ring = newRing
	return true
}

func (r *Ring) GetNode(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.ring) == 0 {
		return ""
	}
	h := hashKey(key)
	idx := sort.Search(len(r.ring), func(i int) bool { return r.ring[i] >= h })
	if idx >= len(r.ring) {
		idx = 0
	}
	return r.ringMap[r.ring[idx]]
}

func (r *Ring) GetNodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	nodes := make([]string, 0, len(r.nodes))
	for node := range r.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

func (r *Ring) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

func hashKey(key string) uint64 {
	return xxhash.Sum64String(key)
}
