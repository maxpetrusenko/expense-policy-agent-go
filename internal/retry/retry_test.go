package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

func testPolicy(slept *[]time.Duration) Policy {
	return Policy{
		MaxAttempts: 4, Base: 100 * time.Millisecond, Max: 400 * time.Millisecond,
		Rand:  func() float64 { return 1 }, // full ceiling, deterministic
		Sleep: func(_ context.Context, d time.Duration) error { *slept = append(*slept, d); return nil },
	}
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	var slept []time.Duration
	calls := 0
	n, err := Do(context.Background(), testPolicy(&slept), func(context.Context, int) error {
		calls++
		if calls < 3 {
			return &failure.StatusError{Status: 503, Err: errors.New("unavailable")}
		}
		return nil
	})
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if len(slept) != 2 || slept[0] != want[0] || slept[1] != want[1] {
		t.Fatalf("backoff = %v want %v", slept, want)
	}
}

func TestNoRetryOnPermanent(t *testing.T) {
	var slept []time.Duration
	n, err := Do(context.Background(), testPolicy(&slept), func(context.Context, int) error {
		return failure.New(failure.Schema, "parse", errors.New("bad"))
	})
	if err == nil || n != 1 || len(slept) != 0 {
		t.Fatalf("n=%d err=%v slept=%v", n, err, slept)
	}
}

func TestExhaustsAttemptsAndCapsBackoff(t *testing.T) {
	var slept []time.Duration
	n, err := Do(context.Background(), testPolicy(&slept), func(context.Context, int) error {
		return &failure.StatusError{Status: 429, Err: errors.New("rate")}
	})
	if err == nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := slept[len(slept)-1]; got != 400*time.Millisecond {
		t.Fatalf("cap not applied: %v", got)
	}
}

func TestJitterWithinBounds(t *testing.T) {
	p := Default()
	for i := 0; i < 200; i++ {
		if d := p.Backoff(3); d < 0 || d > 800*time.Millisecond {
			t.Fatalf("out of range: %v", d)
		}
	}
}

func TestContextCancelStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := Default()
	_, err := Do(ctx, p, func(context.Context, int) error {
		return &failure.StatusError{Status: 500, Err: errors.New("x")}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
