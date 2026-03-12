package origin

import (
	"strings"
	"testing"
)

func TestFetch(t *testing.T) {
	s := NewServer(5, 20)
	resp, err := s.Fetch("/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if len(resp.Body) == 0 {
		t.Fatal("empty body")
	}
	if resp.ContentType == "" {
		t.Fatal("empty content type")
	}
	if resp.ETag == "" {
		t.Fatal("empty etag")
	}
}

func TestContentTypes(t *testing.T) {
	s := NewServer(5, 20)
	tests := []struct {
		path string
		want string
	}{
		{"/a.css", "css"},
		{"/b.js", "javascript"},
		{"/c.json", "json"},
		{"/d.html", "html"},
	}
	for _, tc := range tests {
		resp, err := s.Fetch(tc.path)
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		if !strings.Contains(strings.ToLower(resp.ContentType), tc.want) {
			t.Errorf("%s: ct=%s, want %s", tc.path, resp.ContentType, tc.want)
		}
	}
}

func TestRequestCount(t *testing.T) {
	s := NewServer(5, 20)
	before := s.RequestCount()
	s.Fetch("/x")
	s.Fetch("/y")
	if s.RequestCount()-before != 2 {
		t.Fatalf("want 2 new, got %d", s.RequestCount()-before)
	}
}
