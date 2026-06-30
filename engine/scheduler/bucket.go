package scheduler

import (
	"context"
	"time"
)

// tokenBucket is a hand-rolled rate limiter (stdlib time only, per the D1
// "hand-roll the core" rule). A background goroutine drips one token into a
// buffered channel every 1/rate seconds, up to a burst cap; take() consumes one
// token, blocking until one is available or the context is cancelled.
//
// It throttles provider/node dispatch when Options.RatePerSec > 0. The scheduler
// loop owns the bucket and stops it (closing the drip) on return.
type tokenBucket struct {
	tokens chan struct{}
	done   chan struct{}
}

// newTokenBucket builds a bucket that emits ratePerSec tokens per second, with a
// burst capacity of `burst` tokens (clamped to at least 1). It starts full so
// the first `burst` takes do not block.
func newTokenBucket(ratePerSec float64, burst int) *tokenBucket {
	if burst < 1 {
		burst = 1
	}
	b := &tokenBucket{
		tokens: make(chan struct{}, burst),
		done:   make(chan struct{}),
	}
	// Start full (one burst worth of tokens) so initial dispatch is not delayed.
	for i := 0; i < burst; i++ {
		b.tokens <- struct{}{}
	}
	interval := time.Duration(float64(time.Second) / ratePerSec)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	go b.drip(interval)
	return b
}

// drip adds one token every interval until stopped. A full bucket drops the
// token (non-blocking send), capping the burst.
func (b *tokenBucket) drip(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			select {
			case b.tokens <- struct{}{}:
			default: // bucket full; drop the token
			}
		}
	}
}

// take consumes one token, blocking until one is available, the bucket is
// stopped, or ctx is cancelled.
func (b *tokenBucket) take(ctx context.Context) {
	select {
	case <-b.tokens:
	case <-b.done:
	case <-ctx.Done():
	}
}

// stop ends the drip goroutine. Safe to call once (the loop defers exactly one).
func (b *tokenBucket) stop() {
	close(b.done)
}
