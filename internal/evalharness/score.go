package evalharness

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/pipeline"
)

// CaseResult is the per-case score.
type CaseResult struct {
	ID         string
	Failed     bool   // pipeline returned an error
	FailClass  string // classified error, when Failed
	FieldOK    map[string]bool
	DecisionOK bool
	RulesOK    bool
	ReviewPred bool
	ReviewWant bool
	Latency    time.Duration
	Misses     []string
}

// Report aggregates one prompt version over the golden set.
type Report struct {
	Prompt        string
	Provider      string
	Cases         int
	FieldAcc      float64
	PerField      map[string]float64
	DecisionAcc   float64
	RulesExact    float64
	ReviewPrec    float64
	ReviewRecall  float64
	FailureAcc    float64 // expected rejections classified correctly (1 when none expected)
	P50, P95      time.Duration
	Results       []CaseResult
	DocsWithMiss  int
	ScoredCases   int
	FailureCases  int
	UnexpectedErr int
}

// Run scores p over g. p must have Flags pinned to one prompt version.
func Run(ctx context.Context, g *Golden, p *pipeline.Pipeline, prompt string) *Report {
	rep := &Report{Prompt: prompt, Provider: p.Provider.Name(), Cases: len(g.Cases), PerField: map[string]float64{}}
	fieldHit := map[string]int{}
	var lat []time.Duration
	var tp, fp, fn, failHit int

	for _, c := range g.Cases {
		t := time.Now()
		res, err := p.Process(ctx, pipeline.Document{Source: c.ID, Text: c.Text})
		cr := CaseResult{ID: c.ID, Latency: time.Since(t), FieldOK: map[string]bool{}}
		lat = append(lat, cr.Latency)
		if err != nil {
			cr.Failed, cr.FailClass = true, string(failure.Classify(err))
		}

		if c.ExpectFailure != "" {
			rep.FailureCases++
			if cr.Failed && cr.FailClass == c.ExpectFailure {
				failHit++
			} else {
				cr.Misses = append(cr.Misses, fmt.Sprintf("expected failure %s, got %s", c.ExpectFailure, orNone(cr)))
			}
			rep.Results = append(rep.Results, cr)
			continue
		}

		rep.ScoredCases++
		cr.ReviewWant = c.NeedsReview
		if cr.Failed {
			rep.UnexpectedErr++
			cr.Misses = append(cr.Misses, "unexpected failure "+cr.FailClass)
			if cr.ReviewWant {
				fn++
			}
			for _, f := range expense.FieldNames {
				cr.FieldOK[f] = false
			}
		} else {
			scoreFields(&cr, c.Expected, res)
			cr.DecisionOK = res.Decision == c.Policy.Decision
			got := ruleSet(res.Findings)
			want := slices.Clone(c.Policy.Rules)
			slices.Sort(want)
			cr.RulesOK = slices.Equal(got, want)
			if !cr.DecisionOK || !cr.RulesOK {
				cr.Misses = append(cr.Misses, fmt.Sprintf("policy got %s%v want %s%v", res.Decision, got, c.Policy.Decision, want))
			}
			cr.ReviewPred = res.NeedsReview
			switch {
			case cr.ReviewPred && cr.ReviewWant:
				tp++
			case cr.ReviewPred && !cr.ReviewWant:
				fp++
				cr.Misses = append(cr.Misses, "review flagged unnecessarily "+strings.Join(res.ReviewReasons, ","))
			case !cr.ReviewPred && cr.ReviewWant:
				fn++
				cr.Misses = append(cr.Misses, "review missed")
			}
		}
		for f, ok := range cr.FieldOK {
			if ok {
				fieldHit[f]++
			}
		}
		rep.Results = append(rep.Results, cr)
	}

	rep.aggregate(fieldHit, tp, fp, fn, failHit, lat)
	return rep
}

func (r *Report) aggregate(fieldHit map[string]int, tp, fp, fn, failHit int, lat []time.Duration) {
	n := float64(max(r.ScoredCases, 1))
	total := 0
	for _, f := range expense.FieldNames {
		r.PerField[f] = float64(fieldHit[f]) / n
		total += fieldHit[f]
	}
	r.FieldAcc = float64(total) / (n * float64(len(expense.FieldNames)))
	dec, rules := 0, 0
	for _, c := range r.Results {
		if c.DecisionOK {
			dec++
		}
		if c.RulesOK {
			rules++
		}
		if len(c.Misses) > 0 {
			r.DocsWithMiss++
		}
	}
	r.DecisionAcc, r.RulesExact = float64(dec)/n, float64(rules)/n
	r.ReviewPrec, r.ReviewRecall = ratio(tp, tp+fp), ratio(tp, tp+fn)
	r.FailureAcc = 1
	if r.FailureCases > 0 {
		r.FailureAcc = float64(failHit) / float64(r.FailureCases)
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	r.P50, r.P95 = percentile(lat, 0.50), percentile(lat, 0.95)
}

func scoreFields(cr *CaseResult, want *Fields, res *pipeline.Result) {
	rec := res.Record
	check := func(field string, ok bool, got, exp any) {
		cr.FieldOK[field] = ok
		if !ok {
			cr.Misses = append(cr.Misses, fmt.Sprintf("%s got %v want %v", field, got, exp))
		}
	}
	wantAmt, _ := expense.ParseMoney(want.Amount)
	check("merchant", strings.EqualFold(strings.TrimSpace(rec.Merchant), want.Merchant), rec.Merchant, want.Merchant)
	check("date", rec.Date == want.Date, rec.Date, want.Date)
	check("amount", rec.AmountMinor == wantAmt, expense.FormatMoney(rec.AmountMinor), want.Amount)
	check("currency", rec.Currency == want.Currency, rec.Currency, want.Currency)
	check("category", rec.Category == want.Category, rec.Category, want.Category)
	check("tax", taxEqual(rec.TaxMinor, want.Tax), taxStr(rec.TaxMinor), ptrStr(want.Tax))
	check("attendees", sameSet(rec.Attendees, want.Attendees), rec.Attendees, want.Attendees)
	check("payment_method", rec.PaymentMethod == want.PaymentMethod, rec.PaymentMethod, want.PaymentMethod)
}

func taxEqual(got *int64, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	w, err := expense.ParseMoney(*want)
	return err == nil && *got == w
}

func taxStr(v *int64) string {
	if v == nil {
		return "null"
	}
	return expense.FormatMoney(*v)
}

func ptrStr(v *string) string {
	if v == nil {
		return "null"
	}
	return *v
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

func ruleSet(f []expense.Finding) []string {
	out := []string{}
	for _, x := range f {
		out = append(out, x.Rule)
	}
	slices.Sort(out)
	return out
}

func orNone(c CaseResult) string {
	if c.Failed {
		return c.FailClass
	}
	return "success"
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p*float64(len(sorted))+0.999999) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}
