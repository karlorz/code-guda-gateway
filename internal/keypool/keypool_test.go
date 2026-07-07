package keypool

import "testing"

func TestPoolReturnsKeysRoundRobin(t *testing.T) {
	pool := New([]string{"a", "b"})

	for _, want := range []string{"a", "b", "a"} {
		got, ok := pool.Next()
		if !ok {
			t.Fatalf("Next ok = false, want true")
		}
		if got != want {
			t.Fatalf("Next = %q, want %q", got, want)
		}
	}
}

func TestPoolReportsEmpty(t *testing.T) {
	pool := New(nil)
	if got, ok := pool.Next(); ok || got != "" {
		t.Fatalf("Next = %q, %v; want empty false", got, ok)
	}
	if pool.Len() != 0 {
		t.Fatalf("Len = %d, want 0", pool.Len())
	}
}
