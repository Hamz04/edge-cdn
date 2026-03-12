package cache

import (
	"testing"
	"time"
)

func TestLRU_SetGet(t *testing.T) {
	c := NewLRUCache(100)
	ok := c.Set("/test", []byte("hello"), time.Minute)
	if !ok {
		t.Fatal("set failed")
	}
	val, found := c.Get("/test")
	if !found {
		t.Fatal("miss")
	}
	if string(val) != "hello" {
		t.Fatalf("want hello, got %s", string(val))
	}
}

func TestLRU_Miss(t *testing.T) {
	c := NewLRUCache(100)
	_, found := c.Get("/nope")
	if found {
		t.Fatal("should miss")
	}
}

func TestLRU_Eviction(t *testing.T) {
	c := NewLRUCache(2)
	c.Set("/a", []byte("a"), time.Minute)
	c.Set("/b", []byte("b"), time.Minute)
	c.Set("/c", []byte("c"), time.Minute)
	_, found := c.Get("/a")
	if found {
		t.Fatal("/a should be evicted")
	}
	_, found = c.Get("/b")
	if !found {
		t.Fatal("/b should exist")
	}
}

func TestLRU_Delete(t *testing.T) {
	c := NewLRUCache(100)
	c.Set("/d", []byte("x"), time.Minute)
	c.Delete("/d")
	_, found := c.Get("/d")
	if found {
		t.Fatal("should be deleted")
	}
}
