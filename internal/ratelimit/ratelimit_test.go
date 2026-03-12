package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewLimiter(t *testing.T) {
	l := New(10, 20)
	if l == nil {
		t.Fatal("expected non-nil limiter")
	}
	defer l.Stop()
}

func TestAllowUnderLimit(t *testing.T) {
	l := New(1000, 100)
	defer l.Stop()
	if !l.Allow("client1") {
		t.Fatal("expected Allow to return true under limit")
	}
}

func TestAllowExceedsLimit(t *testing.T) {
	l := New(1, 1)
	defer l.Stop()

	// First should succeed
	l.Allow("client1")
	// Burn through burst
	blocked := false
	for i := 0; i < 20; i++ {
		if !l.Allow("client1") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("expected rate limiter to block eventually")
	}
}

func TestMiddleware(t *testing.T) {
	l := New(1000, 100)
	defer l.Stop()

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
	l := New(1, 1)
	defer l.Stop()

	l.Allow("client1")
	// client2 should have its own bucket
	if !l.Allow("client2") {
		t.Fatal("expected different keys to be independent")
	}
}
