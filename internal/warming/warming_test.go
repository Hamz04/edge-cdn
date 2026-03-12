package warming

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.WarmInterval <= 0 {
		t.Fatal("expected positive WarmInterval")
	}
	if cfg.MaxPrefetchPaths <= 0 {
		t.Fatal("expected positive MaxPrefetchPaths")
	}
}

func TestConfigValues(t *testing.T) {
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
