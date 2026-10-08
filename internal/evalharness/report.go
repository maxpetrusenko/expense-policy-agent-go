package evalharness

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
)

// Thresholds are the minimums a prompt version must meet to pass the gate.
type Thresholds struct {
	FieldAcc     float64
	DecisionAcc  float64
	ReviewRecall float64
	ReviewPrec   float64
	FailureAcc   float64
	MaxP95       time.Duration
	// MaxRegression is how far the candidate's field accuracy and decision
	// accuracy may fall below the baseline.
	MaxRegression float64
}

// Gate returns the list of failed checks for cand. base may be nil.
func Gate(cand, base *Report, t Thresholds) []string {
	var f []string
	chk := func(name string, got, min float64) {
		if got+1e-9 < min {
			f = append(f, fmt.Sprintf("%s %.3f < required %.3f", name, got, min))
		}
	}
	chk("field accuracy", cand.FieldAcc, t.FieldAcc)
	chk("policy decision accuracy", cand.DecisionAcc, t.DecisionAcc)
	chk("review recall", cand.ReviewRecall, t.ReviewRecall)
	chk("review precision", cand.ReviewPrec, t.ReviewPrec)
	chk("failure classification accuracy", cand.FailureAcc, t.FailureAcc)
	if t.MaxP95 > 0 && cand.P95 > t.MaxP95 {
		f = append(f, fmt.Sprintf("latency p95 %s > allowed %s", cand.P95, t.MaxP95))
	}
	if base != nil {
		chk("field accuracy vs baseline "+base.Prompt, cand.FieldAcc, base.FieldAcc-t.MaxRegression)
		chk("policy decision accuracy vs baseline "+base.Prompt, cand.DecisionAcc, base.DecisionAcc-t.MaxRegression)
	}
	return f
}

func pct(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

func dur(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dus", d.Microseconds())
	}
	return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
}

// Print writes a side-by-side table for one or more reports. The last report
// is the candidate; the delta column compares it to the first.
func Print(w io.Writer, g *Golden, reps []*Report, verbose bool) {
	fmt.Fprintf(w, "golden set: %d cases (%d scored, %d expected rejections), now=%s\n", len(g.Cases), reps[0].ScoredCases, reps[0].FailureCases, g.Now)
	fmt.Fprintf(w, "provider:   %s\n\n", reps[0].Provider)

	type row struct {
		name string
		val  func(*Report) string
		num  func(*Report) float64
	}
	rows := []row{
		{"field accuracy", func(r *Report) string { return pct(r.FieldAcc) }, func(r *Report) float64 { return r.FieldAcc }},
	}
	for _, f := range expense.FieldNames {
		rows = append(rows, row{"  " + f, func(r *Report) string { return pct(r.PerField[f]) }, func(r *Report) float64 { return r.PerField[f] }})
	}
	rows = append(rows,
		row{"policy decision accuracy", func(r *Report) string { return pct(r.DecisionAcc) }, func(r *Report) float64 { return r.DecisionAcc }},
		row{"policy rule-set exact match", func(r *Report) string { return pct(r.RulesExact) }, func(r *Report) float64 { return r.RulesExact }},
		row{"review flag precision", func(r *Report) string { return pct(r.ReviewPrec) }, func(r *Report) float64 { return r.ReviewPrec }},
		row{"review flag recall", func(r *Report) string { return pct(r.ReviewRecall) }, func(r *Report) float64 { return r.ReviewRecall }},
		row{"failure classification", func(r *Report) string { return pct(r.FailureAcc) }, func(r *Report) float64 { return r.FailureAcc }},
	)
	fmt.Fprintf(w, "%-30s", "metric")
	for _, r := range reps {
		fmt.Fprintf(w, "%12s", "prompt "+r.Prompt)
	}
	if len(reps) > 1 {
		fmt.Fprintf(w, "%10s", "delta")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("-", 30+12*len(reps)+map[bool]int{true: 10}[len(reps) > 1]))
	for _, rw := range rows {
		fmt.Fprintf(w, "%-30s", rw.name)
		for _, r := range reps {
			fmt.Fprintf(w, "%12s", rw.val(r))
		}
		if len(reps) > 1 {
			fmt.Fprintf(w, "%+9.1fp", (rw.num(reps[len(reps)-1])-rw.num(reps[0]))*100)
		}
		fmt.Fprintln(w)
	}
	for _, lbl := range []struct {
		n string
		f func(*Report) time.Duration
	}{{"latency p50", func(r *Report) time.Duration { return r.P50 }}, {"latency p95", func(r *Report) time.Duration { return r.P95 }}} {
		fmt.Fprintf(w, "%-30s", lbl.n)
		for _, r := range reps {
			fmt.Fprintf(w, "%12s", dur(lbl.f(r)))
		}
		fmt.Fprintln(w)
	}
	for _, r := range reps {
		printMisses(w, r, verbose)
	}
}

func printMisses(w io.Writer, r *Report, verbose bool) {
	fmt.Fprintf(w, "\nprompt %s: %d of %d cases with at least one miss\n", r.Prompt, r.DocsWithMiss, r.Cases)
	shown := 0
	for _, c := range r.Results {
		if len(c.Misses) == 0 {
			continue
		}
		if !verbose && shown >= 8 {
			fmt.Fprintln(w, "  ... (use -v for all)")
			return
		}
		shown++
		fmt.Fprintf(w, "  %s: %s\n", c.ID, strings.Join(c.Misses, "; "))
	}
}
