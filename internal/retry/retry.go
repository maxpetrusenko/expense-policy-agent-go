// Package retry runs an operation with bounded attempts, exponential backoff
// and full jitter. Only errors classified as retryable are retried.
package retry

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

// Policy bounds the retry loop.
type Policy struct {
	MaxAttempts int
	Base        time.Duration
	Max         time.Duration
	// Sleep and Rand are injectable for tests.
	Sleep func(context.Context, time.Duration) error
	Rand  func() float64
}

// Default is 4 attempts, 200ms base, 5s cap.
func Default() Policy {
	return Policy{MaxAttempts: 4, Base: 200 * time.Millisecond, Max: 5 * time.Second}
}

// Backoff returns the delay before attempt n+1 (n is the 1-based failed attempt):
// a uniform random value in [0, min(Max, Base*2^(n-1))].
func (p Policy) Backoff(n int) time.Duration {
	ceil := p.Base << (n - 1)
	if ceil <= 0 || ceil > p.Max {
		ceil = p.Max
	}
	r := rand.Float64
	if p.Rand != nil {
		r = p.Rand
	}
	return time.Duration(r() * float64(ceil))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do calls fn until it succeeds, returns a non-retryable error, the attempts
// run out, or ctx ends. It returns the number of attempts made.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context, attempt int) error) (int, error) {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	sl := p.Sleep
	if sl == nil {
		sl = sleep
	}
	var err error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		err = fn(ctx, attempt)
		if err == nil {
			return attempt, nil
		}
		if !failure.IsRetryable(err) || attempt == p.MaxAttempts {
			return attempt, err
		}
		if serr := sl(ctx, p.Backoff(attempt)); serr != nil {
			return attempt, serr
		}
	}
	return p.MaxAttempts, err
}
