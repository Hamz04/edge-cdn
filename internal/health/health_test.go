package health

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNewChecker(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := Config{
		Interval:         100 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		FailThreshold:    2,
		SuccessThreshold: 1,
	}
	c := New(cfg, logger)
	if c == nil {
		t.Fatal("expected non-nil checker")
	}
}

func TestRegisterAndCheckNode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := Config{
		Interval:         100 * time.Millisecond,
		Timeout:          50 * time.Millisecond,
		FailThreshold:    2,
		SuccessThreshold: 1,
	}
	c := New(cfg, logger)
	c.RegisterNode("127.0.0.1:8080", "us-east")

	// Before starting health checks, node status is initial
	allHealth := c.GetAllHealth()
	if len(allHealth) == 0 {
		t.Fatal("expected at least one registered node")
	}
}

func TestHealthHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := Config{
		Interval:         time.Second,
		Timeout:          time.Second,
		FailThreshold:    3,
		SuccessThreshold: 1,
	}
	c := New(cfg, logger)

	handler := c.Handler()
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestStatusHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := Config{
		Interval:         time.Second,
		Timeout:          time.Second,
		FailThreshold:    3,
		SuccessThreshold: 1,
	}
	c := New(cfg, logger)
	c.RegisterNode("127.0.0.1:8080", "us-east")

	handler := c.StatusHandler()
	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestUnregisterNode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cfg := Config{
		Interval:         time.Second,
		Timeout:          time.Second,
		FailThreshold:    3,
		SuccessThreshold: 1,
	}
	c := New(cfg, logger)
	c.RegisterNode("127.0.0.1:8080", "us-east")
	c.UnregisterNode("127.0.0.1:8080")

	allHealth := c.GetAllHealth()
	if len(allHealth) != 0 {
		t.Fatalf("expected 0 nodes after unregister, got %d", len(allHealth))
	}
}
