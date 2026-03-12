package health

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

var (
	testChecker     *Checker
	testCheckerOnce sync.Once
	testLogger      *slog.Logger
)

func getTestChecker() *Checker {
	testCheckerOnce.Do(func() {
		testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
		testChecker = New(Config{
			Interval:         100 * time.Millisecond,
			Timeout:          50 * time.Millisecond,
			FailThreshold:    2,
			SuccessThreshold: 1,
		}, testLogger)
	})
	return testChecker
}

func TestNewChecker(t *testing.T) {
	c := getTestChecker()
	if c == nil {
		t.Fatal("expected non-nil checker")
	}
}

func TestRegisterAndCheckNode(t *testing.T) {
	c := getTestChecker()
	c.RegisterNode("127.0.0.1:9090", "us-east")
	allHealth := c.GetAllHealth()
	if len(allHealth) == 0 {
		t.Fatal("expected at least one registered node")
	}
}

func TestHealthHandler(t *testing.T) {
	c := getTestChecker()
	handler := c.Handler()
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestStatusHandler(t *testing.T) {
	c := getTestChecker()
	handler := c.StatusHandler()
	req := httptest.NewRequest("GET", "/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestUnregisterNode(t *testing.T) {
	c := getTestChecker()
	c.RegisterNode("127.0.0.1:7070", "eu-west")
	c.UnregisterNode("127.0.0.1:7070")
	// Just verify no panic
}
