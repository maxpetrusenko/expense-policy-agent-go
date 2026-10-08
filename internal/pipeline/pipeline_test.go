package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/flags"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/policy"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/provider"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/retry"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/store"
)

const receipt = `Harbor Grill Restaurant
Date: 2026-06-10
Burger 18.00
Salad 14.00
Subtotal 32.00
Tax 2.60
Total $34.60
Guests: Ana, Ben
VISA ****1111`

func newPipe(t *testing.T, prov provider.Provider) *Pipeline {
	t.Helper()
	pol, err := policy.Load("../../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	idem, _ := store.OpenIdempotency(filepath.Join(t.TempDir(), "idem.json"))
	return &Pipeline{
		Provider: prov, Policy: pol, Flags: flags.Flags{Stable: "v2"},
		Retry: retry.Policy{MaxAttempts: 3, Base: time.Millisecond, Max: time.Millisecond,
			Sleep: func(context.Context, time.Duration) error { return nil }},
		Idem: idem, DLQ: &store.DLQ{Dir: filepath.Join(t.TempDir(), "dlq")},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC) },
	}
}

func TestHappyPath(t *testing.T) {
	p := newPipe(t, provider.Mock{})
	r, err := p.Process(context.Background(), Document{Source: "a.txt", Text: receipt})
	if err != nil {
		t.Fatal(err)
	}
	if r.Record.AmountMinor != 3460 || *r.Record.TaxMinor != 260 || r.Decision != "pass" || r.NeedsReview {
		t.Fatalf("%+v", r)
	}
	if r.TimingsMs["extract"] < 0 || r.TimingsMs["total"] <= 0 {
		t.Fatalf("timings %v", r.TimingsMs)
	}
}

func TestIdempotentSkip(t *testing.T) {
	ff := &provider.FailFirst{Inner: provider.Mock{}}
	p := newPipe(t, ff)
	if _, err := p.Process(context.Background(), Document{Text: receipt}); err != nil {
		t.Fatal(err)
	}
	r, err := p.Process(context.Background(), Document{Text: receipt})
	if err != nil || !r.Cached || ff.Calls() != 1 {
		t.Fatalf("cached=%v calls=%d err=%v", r.Cached, ff.Calls(), err)
	}
	p.Force = true
	if _, err := p.Process(context.Background(), Document{Text: receipt}); err != nil || ff.Calls() != 2 {
		t.Fatalf("force should reprocess: calls=%d err=%v", ff.Calls(), err)
	}
	// a new policy version is a new key
	p.Force = false
	p.Policy.Version = "2026.3"
	if r, _ := p.Process(context.Background(), Document{Text: receipt}); r.Cached {
		t.Fatal("policy version change must bypass the cache")
	}
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	ff := &provider.FailFirst{Inner: provider.Mock{}, N: 2, Err: &failure.StatusError{Status: 429, Err: errors.New("rate")}}
	p := newPipe(t, ff)
	r, err := p.Process(context.Background(), Document{Text: receipt})
	if err != nil || r.Attempts != 3 {
		t.Fatalf("attempts=%v err=%v", r, err)
	}
}

func TestRetryExhaustionGoesToDLQ(t *testing.T) {
	ff := &provider.FailFirst{Inner: provider.Mock{}, N: 99, Err: &failure.StatusError{Status: 503, Err: errors.New("down")}}
	p := newPipe(t, ff)
	_, err := p.Process(context.Background(), Document{Source: "x.txt", Text: receipt})
	if failure.Classify(err) != failure.Retryable || ff.Calls() != 3 {
		t.Fatalf("calls=%d err=%v", ff.Calls(), err)
	}
	dl, _ := p.DLQ.List()
	if len(dl) != 1 || dl[0].Class != "retryable" || dl[0].Attempts != 3 {
		t.Fatalf("%+v", dl)
	}
	if p.Idem.Len() != 0 {
		t.Fatal("failed documents must not be marked done")
	}
}

func TestSchemaViolationNotRetried(t *testing.T) {
	ff := &provider.FailFirst{Inner: provider.Mock{}}
	p := newPipe(t, ff)
	_, err := p.Process(context.Background(), Document{Source: "bad.txt", Text: provider.HookSchema})
	if failure.Classify(err) != failure.Schema || ff.Calls() != 1 {
		t.Fatalf("calls=%d err=%v", ff.Calls(), err)
	}
	dl, _ := p.DLQ.List()
	if len(dl) != 1 || dl[0].RawOutput == "" || dl[0].Stage != "extract" {
		t.Fatalf("%+v", dl)
	}
}

func TestInvalidDocuments(t *testing.T) {
	p := newPipe(t, provider.Mock{})
	for name, doc := range map[string]Document{
		"empty":       {Source: "e.txt"},
		"no amount":   {Source: "n.txt", Text: "Just a note about lunch, no numbers"},
		"missing img": {Source: "i.txt", Text: "x", ImagePath: "/nonexistent.png"},
	} {
		_, err := p.Process(context.Background(), doc)
		if failure.Classify(err) != failure.InvalidDoc {
			t.Errorf("%s: %v", name, err)
		}
	}
	if dl, _ := p.DLQ.List(); len(dl) != 3 {
		t.Fatalf("dlq has %d entries", len(dl))
	}
}

func TestFlagSwitchesPromptVersion(t *testing.T) {
	p := newPipe(t, provider.Mock{})
	p.Flags = flags.Flags{Stable: "v1"}
	r, _ := p.Process(context.Background(), Document{Text: receipt})
	if r.PromptVersion != "v1" || r.Record.AmountMinor != 3200 {
		t.Fatalf("v1 should pick the subtotal: %+v", r.Record)
	}
	p.Flags = flags.Flags{Stable: "v1", Candidate: "v2", Percent: 100}
	p.Force = true
	r, _ = p.Process(context.Background(), Document{Text: receipt})
	if r.PromptVersion != "v2" || r.Record.AmountMinor != 3460 {
		t.Fatalf("candidate at 100%%: %+v", r)
	}
}

func TestReviewRouting(t *testing.T) {
	p := newPipe(t, provider.Mock{})
	// ambiguous date (03/04) has confidence 0.5, below the 0.70 threshold
	r, err := p.Process(context.Background(), Document{Text: "Corner Cafe\n03/04/2026\nCoffee 4.00\nTotal 4.00\nCash"})
	if err != nil || !r.NeedsReview {
		t.Fatalf("%+v %v", r, err)
	}
	found := false
	for _, x := range r.ReviewReasons {
		found = found || x == "low_confidence:date"
	}
	if !found {
		t.Fatalf("reasons %v", r.ReviewReasons)
	}
}
