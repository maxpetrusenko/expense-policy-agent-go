// Package validate runs deterministic checks on the model's extraction and
// produces a typed Record. Hard failures (no usable amount) return an error;
// everything else becomes an Issue that forces human review.
package validate

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

// currencies is the set of ISO 4217 codes accepted for expenses.
var currencies = map[string]bool{
	"USD": true, "EUR": true, "GBP": true, "CAD": true, "AUD": true, "JPY": true, "CHF": true,
	"MXN": true, "BRL": true, "INR": true, "CNY": true, "SGD": true, "HKD": true, "SEK": true,
	"NOK": true, "DKK": true, "NZD": true, "ZAR": true, "AED": true, "PLN": true, "CZK": true,
}

// MaxAge is how far back a transaction date is considered sane.
const MaxAge = 400 * 24 * time.Hour

// Run validates ex against the clock "now".
func Run(ex expense.Extraction, now time.Time) (expense.Record, []expense.Issue, error) {
	rec := expense.Record{
		Merchant:        strings.TrimSpace(ex.Merchant),
		Category:        ex.Category,
		Attendees:       cleanNames(ex.Attendees),
		PaymentMethod:   ex.PaymentMethod,
		DocumentType:    ex.DocumentType,
		ContainsAlcohol: ex.ContainsAlcohol,
	}
	var issues []expense.Issue

	amt, err := expense.ParseMoney(ex.Amount)
	if err != nil {
		return rec, nil, failure.New(failure.InvalidDoc, "validate", fmt.Errorf("amount %q: %w", ex.Amount, err))
	}
	if amt <= 0 {
		return rec, nil, failure.New(failure.InvalidDoc, "validate", errors.New("amount must be positive"))
	}
	rec.AmountMinor = amt

	if rec.Merchant == "" {
		issues = append(issues, expense.Issue{Field: "merchant", Code: "missing", Message: "merchant is empty"})
	}

	cur := strings.ToUpper(strings.TrimSpace(ex.Currency))
	if currencies[cur] {
		rec.Currency = cur
	} else {
		issues = append(issues, expense.Issue{Field: "currency", Code: "invalid_code", Message: fmt.Sprintf("%q is not a supported ISO 4217 code", ex.Currency)})
	}

	if iss := checkDate(&rec, ex.Date, now); iss != nil {
		issues = append(issues, *iss)
	}

	if ex.Tax != nil {
		t, err := expense.ParseMoney(*ex.Tax)
		switch {
		case err != nil:
			issues = append(issues, expense.Issue{Field: "tax", Code: "unparseable", Message: err.Error()})
		case t < 0 || t >= amt:
			issues = append(issues, expense.Issue{Field: "tax", Code: "out_of_range", Message: "tax must be >= 0 and less than the total"})
		default:
			rec.TaxMinor = &t
		}
	}
	return rec, issues, nil
}

func checkDate(rec *expense.Record, s string, now time.Time) *expense.Issue {
	s = strings.TrimSpace(s)
	if s == "" {
		return &expense.Issue{Field: "date", Code: "missing", Message: "no date found"}
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return &expense.Issue{Field: "date", Code: "unparseable", Message: fmt.Sprintf("%q is not YYYY-MM-DD", s)}
	}
	if d.After(now) {
		return &expense.Issue{Field: "date", Code: "future", Message: fmt.Sprintf("%s is in the future", s)}
	}
	if now.Sub(d) > MaxAge {
		return &expense.Issue{Field: "date", Code: "too_old", Message: fmt.Sprintf("%s is more than 400 days old", s)}
	}
	rec.Date = s
	return nil
}

func cleanNames(in []string) []string {
	out := []string{}
	for _, n := range in {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}
