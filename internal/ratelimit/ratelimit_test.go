package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

var (
	testLimiter     *Limiter
	testLimiterOnce sync.Once
)

func getTestLimiter() *Limiter {
	testLimiterOnce.Do(func() {
		testLimiter = New(1000, 100)
	})
	return testLimiter
}

func TestNewLimiter(t *testing.T) {
	l := getTestLimiter()
	if l == nil {
		t.Fatal("expected non-nil limiter")
	}
}

func TestAllowUnderLimit(t *testing.T) {
	l := getTestLimiter()
	if !l.Allow("test-client-1") {
		t.Fatal("expected Allow to return true under limit")
	}
}

func TestMiddleware(t *testing.T) {
	l := getTestLimiter()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := l.Middleware(inner)
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDifferentKeysIndependent(t *testing.T) {
	l := getTestLimiter()
	l.Allow("client-a")
	if !l.Allow("client-b") {
		t.Fatal("expected different keys to be independent")
	}
}
