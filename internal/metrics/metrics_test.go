package metrics

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var (
	testMetrics     *Metrics
	testMetricsOnce sync.Once
)

func getTestMetrics() *Metrics {
	testMetricsOnce.Do(func() {
		testMetrics = New("test-node")
	})
	return testMetrics
}

func TestNewMetrics(t *testing.T) {
	m := getTestMetrics()
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}
}

func TestRecordRequest(t *testing.T) {
	m := getTestMetrics()
	m.RecordRequest("GET", "/test", "200", "HIT", 5*time.Millisecond)
	m.RecordRequest("POST", "/api", "500", "MISS", 100*time.Millisecond)
}

func TestPrometheusHandler(t *testing.T) {
	m := getTestMetrics()
	m.RecordRequest("GET", "/", "200", "HIT", time.Millisecond)
	handler := m.PrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestStatsHandler(t *testing.T) {
	m := getTestMetrics()
	handler := m.StatsHandler()
	req := httptest.NewRequest("GET", "/stats", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
