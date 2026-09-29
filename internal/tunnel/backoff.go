package tunnel

import (
	"math/rand"
	"time"
)

// Backoff grows exponentially with jitter and is reset after a stable run.
type Backoff struct {
	Base    time.Duration
	Max     time.Duration
	attempt int
}

// Next returns the delay to wait before the next attempt.
func (b *Backoff) Next() time.Duration {
	d := b.Base << b.attempt
	if d > b.Max || d <= 0 {
		d = b.Max
	} else {
		b.attempt++
	}
	jitter := time.Duration(rand.Int63n(int64(d)/5+1)) - d/10
	return d + jitter
}

// Reset forgets previous failures.
func (b *Backoff) Reset() {
	b.attempt = 0
}
