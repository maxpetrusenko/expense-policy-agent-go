package evalharness

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/flags"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/pipeline"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/policy"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/provider"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/retry"
)

func score(t *testing.T, version string) (*Golden, *Report) {
	t.Helper()
	g, err := LoadGolden("../../eval/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Load("../../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse("2006-01-02", g.Now)
	p := &pipeline.Pipeline{Provider: provider.Mock{}, Policy: pol, Flags: flags.Flags{Stable: version}, Retry: retry.Default(),
		Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return g, Run(context.Background(), g, p, version)
}

var thresholds = Thresholds{FieldAcc: 0.93, DecisionAcc: 0.90, ReviewRecall: 0.95, ReviewPrec: 0.90, FailureAcc: 1, MaxP95: 5 * time.Second}

func TestGoldenSetShape(t *testing.T) {
	g, _ := score(t, "v2")
	if len(g.Cases) < 25 {
		t.Fatalf("golden set has %d cases, want >= 25", len(g.Cases))
	}
	for _, c := range g.Cases {
		if strings.Contains(strings.ToLower(c.Text), "@") {
			t.Errorf("%s: golden text must not contain email addresses", c.ID)
		}
	}
}

func TestCandidateV2PassesGate(t *testing.T) {
	_, v1 := score(t, "v1")
	_, v2 := score(t, "v2")
	if fails := Gate(v2, v1, thresholds); len(fails) != 0 {
		t.Fatalf("v2 should pass: %v", fails)
	}
}

func TestWeakPromptFailsGate(t *testing.T) {
	_, v1 := score(t, "v1")
	_, v2 := score(t, "v2")
	fails := Gate(v1, nil, thresholds)
	if len(fails) == 0 {
		t.Fatal("v1 must fail the gate; the gate would not catch a bad prompt")
	}
	// and a regression against a better baseline is caught even if thresholds are loose
	loose := Thresholds{MaxRegression: 0.01}
	if fails := Gate(v1, v2, loose); len(fails) == 0 {
		t.Fatal("regression vs baseline must fail")
	}
}

func TestMetricsAreComputedNotAssumed(t *testing.T) {
	_, r := score(t, "v2")
	if r.ScoredCases != 24 || r.FailureCases != 2 || r.FailureAcc != 1 {
		t.Fatalf("%+v", r)
	}
	if r.FieldAcc >= 1 || r.DecisionAcc >= 1 {
		t.Fatal("known hard cases (r19, r20) must register as misses")
	}
	if r.P95 < r.P50 {
		t.Fatal("p95 < p50")
	}
}

func TestPercentile(t *testing.T) {
	d := []time.Duration{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if percentile(d, 0.5) != 5 || percentile(d, 0.95) != 10 || percentile(nil, 0.5) != 0 {
		t.Fatal("percentile")
	}
}

func TestLoadGoldenRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"empty":     `{"cases":[]}`,
		"dup":       `{"cases":[{"id":"a","expect_failure":"x"},{"id":"a","expect_failure":"x"}]}`,
		"no expect": `{"cases":[{"id":"a","text":"x"}]}`,
	} {
		p := filepath.Join(dir, name+".json")
		_ = os.WriteFile(p, []byte(body), 0o600)
		if _, err := LoadGolden(p); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
