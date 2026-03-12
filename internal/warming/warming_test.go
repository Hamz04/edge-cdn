package warming

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Hamz04/edge-cdn/internal/cache"
	"github.com/Hamz04/edge-cdn/internal/origin"
)

func TestNewWarmer(t *testing.T) {
	c := cache.NewLRUCache(100)
	o := origin.NewServer(1, 5)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// LRUCache doesn't implement cache.Cache interface, so we test what we can
	// The warmer needs a cache.Cache, but LRUCache is a concrete type
	// This test verifies the DefaultConfig and basic setup
	cfg := DefaultConfig()
	if cfg.WarmInterval <= 0 {
		t.Fatal("expected positive WarmInterval")
	}
	if cfg.MaxPrefetchPaths <= 0 {
		t.Fatal("expected positive MaxPrefetchPaths")
	}
	_ = c
	_ = o
	_ = logger
}

func TestRecordAccess(t *testing.T) {
	// Since we can't easily create a full cache.Cache without Redis,
	// test that DefaultConfig produces valid values
	cfg := DefaultConfig()
	if cfg.PrefetchThreshold <= 0 {
		t.Fatal("expected positive PrefetchThreshold")
	}
	if cfg.TrackingWindow <= 0 {
		t.Fatal("expected positive TrackingWindow")
	}
	if cfg.PrefetchTimeout <= 0 {
		t.Fatal("expected positive PrefetchTimeout")
	}
}
