package metrics

import "testing"

func TestNew(t *testing.T) {
	m := New("test")
	if m == nil {
		t.Fatal("nil")
	}
}
