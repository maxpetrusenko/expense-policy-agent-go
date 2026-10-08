// Package policy is the deterministic compliance engine. It never calls a
// model: the same Record and Policy always produce the same findings.
package policy

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
)

// Policy mirrors the YAML file.
type Policy struct {
	Version        string             `yaml:"version"`
	BaseCurrency   string             `yaml:"base_currency"`
	CategoryLimits map[string]float64 `yaml:"category_limits"`
	PerDiem        map[string]float64 `yaml:"per_diem"`
	ReceiptAbove   float64            `yaml:"receipt_required_above"`
	Attendees      struct {
		Above      float64  `yaml:"above"`
		Categories []string `yaml:"categories"`
	} `yaml:"attendees_required"`
	WeekendCategories []string `yaml:"weekend_review_categories"`
	Alcohol           string   `yaml:"alcohol"`
	MaxAgeDays        int      `yaml:"max_age_days"`
	Review            struct {
		MinConfidence float64 `yaml:"min_confidence"`
	} `yaml:"review"`
}

// Load reads and validates a policy file.
func Load(path string) (*Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Policy
	dec := yaml.NewDecoder(bytesReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &p, p.validate()
}

func (p *Policy) validate() error {
	switch {
	case p.Version == "":
		return fmt.Errorf("policy: version is required")
	case p.BaseCurrency == "":
		return fmt.Errorf("policy: base_currency is required")
	case p.Alcohol != "warn" && p.Alcohol != "violation":
		return fmt.Errorf("policy: alcohol must be warn or violation, got %q", p.Alcohol)
	case p.Review.MinConfidence <= 0 || p.Review.MinConfidence > 1:
		return fmt.Errorf("policy: review.min_confidence must be in (0,1]")
	}
	for c := range p.CategoryLimits {
		if !slices.Contains(expense.Categories, c) {
			return fmt.Errorf("policy: unknown category %q in category_limits", c)
		}
	}
	return nil
}

func minor(v float64) int64 { return int64(v*100 + 0.5) }

// Result is the engine output.
type Result struct {
	Decision string
	Findings []expense.Finding
}

// Evaluate applies every rule to rec. today is the processing date used for
// the age rule.
func (p *Policy) Evaluate(rec expense.Record, today string) Result {
	var f []expense.Finding
	add := func(rule, sev, format string, a ...any) {
		f = append(f, expense.Finding{Rule: rule, Severity: sev, Message: fmt.Sprintf(format, a...)})
	}
	sameCur := rec.Currency == p.BaseCurrency

	if rec.Currency != "" && !sameCur {
		add("FX_REVIEW", expense.DecisionWarn, "currency %s differs from base %s; limits not applied until converted", rec.Currency, p.BaseCurrency)
	}
	if sameCur {
		if lim, ok := p.CategoryLimits[rec.Category]; ok && rec.AmountMinor > minor(lim) {
			add("CATEGORY_LIMIT", expense.DecisionViolation, "%s %s exceeds %s limit %s", rec.Category, expense.FormatMoney(rec.AmountMinor), rec.Category, expense.FormatMoney(minor(lim)))
		}
		if pd, ok := p.PerDiem[rec.Category]; ok {
			people := int64(max(len(rec.Attendees), 1))
			if rec.AmountMinor/people > minor(pd) {
				add("PER_DIEM", expense.DecisionViolation, "%s per person exceeds per-diem %s (%d people)", expense.FormatMoney(rec.AmountMinor/people), expense.FormatMoney(minor(pd)), people)
			}
		}
		if rec.AmountMinor > minor(p.ReceiptAbove) && rec.DocumentType != "itemized_receipt" && rec.DocumentType != "invoice" {
			add("RECEIPT_REQUIRED", expense.DecisionViolation, "itemized receipt or invoice required above %s, got %s", expense.FormatMoney(minor(p.ReceiptAbove)), rec.DocumentType)
		}
		if slices.Contains(p.Attendees.Categories, rec.Category) && rec.AmountMinor > minor(p.Attendees.Above) && len(rec.Attendees) == 0 {
			add("ATTENDEES_REQUIRED", expense.DecisionViolation, "%s above %s must list attendees", rec.Category, expense.FormatMoney(minor(p.Attendees.Above)))
		}
	}
	if rec.ContainsAlcohol {
		add("ALCOHOL", p.Alcohol, "alcohol on the receipt is not reimbursable")
	}
	if rec.Date != "" {
		p.dateRules(rec, today, add)
	}
	return Result{Decision: decide(f), Findings: f}
}

func decide(f []expense.Finding) string {
	d := expense.DecisionPass
	for _, x := range f {
		if x.Severity == expense.DecisionViolation {
			return expense.DecisionViolation
		}
		d = expense.DecisionWarn
	}
	return d
}
