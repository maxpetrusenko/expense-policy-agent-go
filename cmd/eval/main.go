// Command eval scores one or more prompt versions against the golden set and
// exits non-zero if the candidate (the last version listed) misses a
// threshold or regresses against the first version listed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/evalharness"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/flags"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/pipeline"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/policy"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/provider"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/retry"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		golden   = fs.String("golden", "eval/golden.json", "golden set")
		polPath  = fs.String("policy", "policies/default.yaml", "policy YAML")
		prompts  = fs.String("prompts", "v1,v2", "comma-separated prompt versions; last is the candidate, first is the baseline")
		provName = fs.String("provider", "", "mock (default), anthropic, openai; or EXPENSE_PROVIDER")
		model    = fs.String("model", "", "model name; or EXPENSE_MODEL")
		baseURL  = fs.String("base-url", "", "OpenAI-compatible base URL; or OPENAI_BASE_URL")
		verbose  = fs.Bool("v", false, "list every miss")
		fieldMin = fs.Float64("min-field-acc", 0.93, "minimum field-level accuracy")
		decMin   = fs.Float64("min-policy-acc", 0.90, "minimum policy decision accuracy")
		recMin   = fs.Float64("min-review-recall", 0.95, "minimum review-flag recall")
		precMin  = fs.Float64("min-review-precision", 0.90, "minimum review-flag precision")
		failMin  = fs.Float64("min-failure-acc", 1.0, "minimum failure classification accuracy")
		p95Max   = fs.Duration("max-p95", 5*time.Second, "maximum p95 latency per document")
		regress  = fs.Float64("max-regression", 0, "allowed drop vs baseline in field and decision accuracy")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	g, err := evalharness.LoadGolden(*golden)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	pol, err := policy.Load(*polPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	prov, err := provider.New(provider.FromEnv(*provName, *model, *baseURL))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	now, err := time.Parse("2006-01-02", g.Now)
	if err != nil {
		fmt.Fprintln(stderr, "error: golden now:", err)
		return 2
	}

	var reps []*evalharness.Report
	for _, v := range strings.Split(*prompts, ",") {
		v = strings.TrimSpace(v)
		fl := flags.Flags{Stable: v}
		if err := fl.Validate(); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		p := &pipeline.Pipeline{
			Provider: prov, Policy: pol, Flags: fl, Retry: retry.Default(),
			Now: func() time.Time { return now },
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		reps = append(reps, evalharness.Run(context.Background(), g, p, v))
	}
	evalharness.Print(stdout, g, reps, *verbose)

	cand := reps[len(reps)-1]
	var base *evalharness.Report
	if len(reps) > 1 {
		base = reps[0]
	}
	fails := evalharness.Gate(cand, base, evalharness.Thresholds{
		FieldAcc: *fieldMin, DecisionAcc: *decMin, ReviewRecall: *recMin, ReviewPrec: *precMin,
		FailureAcc: *failMin, MaxP95: *p95Max, MaxRegression: *regress,
	})
	if len(fails) > 0 {
		fmt.Fprintf(stdout, "\nGATE FAILED for prompt %s:\n", cand.Prompt)
		for _, f := range fails {
			fmt.Fprintln(stdout, "  -", f)
		}
		return 1
	}
	fmt.Fprintf(stdout, "\nGATE PASSED for prompt %s\n", cand.Prompt)
	return 0
}
