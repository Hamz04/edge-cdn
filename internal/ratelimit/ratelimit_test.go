package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllowWithinBurst(t *testing.T) {
	rl := New(10, 10)
	defer rl.Stop()
	for i := 0; i < 10; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
}

func TestBlocksOverLimit(t *testing.T) {
	rl := New(1, 2)
	defer rl.Stop()
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")
	if rl.Allow("10.0.0.1") {
		t.Fatal("should be limited")
	}
}

func TestPerIPIsolation(t *testing.T) {
	rl := New(1, 1)
	defer rl.Stop()
	rl.Allow("10.0.0.1")
	if !rl.Allow("10.0.0.2") {
		t.Fatal("different IP should pass")
	}
}

func TestMiddleware(t *testing.T) {
	rl := New(1, 1)
	defer rl.Stop()

	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:9999"

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req)
	if w1.Code != 200 {
		t.Fatalf("want 200, got %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req)
	if w2.Code != 429 {
		t.Fatalf("want 429, got %d", w2.Code)
	}
}
