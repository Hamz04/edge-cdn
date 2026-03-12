package cache

import (
	"testing"
	"time"
)

func TestNewLRUCache(t *testing.T) {
	c := NewLRUCache(100)
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if c.Len() != 0 {
		t.Fatalf("expected empty cache, got len %d", c.Len())
	}
}

func TestSetAndGet(t *testing.T) {
	c := NewLRUCache(10)
	val := []byte("hello world")
	c.Set("key1", val, time.Minute)

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != "hello world" {
		t.Fatalf("expected hello world, got %s", string(got))
	}
}

func TestGetMiss(t *testing.T) {
	c := NewLRUCache(10)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestDelete(t *testing.T) {
	c := NewLRUCache(10)
	c.Set("key1", []byte("data"), time.Minute)
	c.Delete("key1")

	_, ok := c.Get("key1")
	if ok {
		t.Fatal("expected miss after delete")
	}
}

func TestTTLExpiry(t *testing.T) {
	c := NewLRUCache(10)
	c.Set("key1", []byte("data"), 20*time.Millisecond)

	time.Sleep(40 * time.Millisecond)
	_, ok := c.Get("key1")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestEviction(t *testing.T) {
	c := NewLRUCache(2)
	c.Set("a", []byte("1"), time.Minute)
	c.Set("b", []byte("2"), time.Minute)
	c.Set("c", []byte("3"), time.Minute) // should evict "a"

	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected a to be evicted")
	}
	_, ok = c.Get("b")
	if !ok {
		t.Fatal("expected b to still exist")
	}
	_, ok = c.Get("c")
	if !ok {
		t.Fatal("expected c to still exist")
	}
}
