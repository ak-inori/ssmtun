package tunnel

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := &Backoff{Base: time.Second, Max: 8 * time.Second}
	prev := time.Duration(0)
	for i := 0; i < 6; i++ {
		d := b.Next()
		if d < 700*time.Millisecond || d > 9*time.Second {
			t.Fatalf("attempt %d: %v out of range", i, d)
		}
		if i < 3 && d < prev {
			t.Fatalf("attempt %d: %v did not grow from %v", i, d, prev)
		}
		prev = d
	}
	b.Reset()
	if d := b.Next(); d > 1300*time.Millisecond {
		t.Fatalf("after reset expected ~1s, got %v", d)
	}
}
