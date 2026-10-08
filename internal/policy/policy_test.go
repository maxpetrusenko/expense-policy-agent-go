package policy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
)

func load(t *testing.T) *Policy {
	t.Helper()
	p, err := Load("../../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func rules(r Result) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Rule)
	}
	slices.Sort(out)
	return out
}

func rec(mod func(*expense.Record)) expense.Record {
	r := expense.Record{Merchant: "X", Date: "2026-06-10", AmountMinor: 4000, Currency: "USD", Category: "meals",
		Attendees: []string{"A", "B"}, DocumentType: "itemized_receipt"}
	mod(&r)
	return r
}

func TestRules(t *testing.T) {
	p := load(t)
	today := "2026-06-30"
	cases := []struct {
		name string
		mod  func(*expense.Record)
		want []string
		dec  string
	}{
		{"clean", func(r *expense.Record) {}, nil, "pass"},
		{"category limit", func(r *expense.Record) { r.Category = "transport"; r.AmountMinor = 12001 }, []string{"CATEGORY_LIMIT"}, "violation"},
		{"at limit ok", func(r *expense.Record) { r.Category = "transport"; r.AmountMinor = 12000 }, nil, "pass"},
		{"per diem solo", func(r *expense.Record) { r.Attendees = []string{"A"}; r.AmountMinor = 6100 }, []string{"PER_DIEM"}, "violation"},
		{"per diem split ok", func(r *expense.Record) { r.AmountMinor = 12000 }, nil, "pass"},
		{"receipt required", func(r *expense.Record) { r.DocumentType = "card_slip" }, []string{"RECEIPT_REQUIRED"}, "violation"},
		{"small slip ok", func(r *expense.Record) { r.DocumentType = "card_slip"; r.AmountMinor = 2500 }, nil, "pass"},
		{"attendees", func(r *expense.Record) { r.Attendees = nil }, []string{"ATTENDEES_REQUIRED"}, "violation"},
		{"alcohol", func(r *expense.Record) { r.ContainsAlcohol = true }, []string{"ALCOHOL"}, "violation"},
		{"weekend", func(r *expense.Record) { r.Date = "2026-06-13" }, []string{"WEEKEND"}, "warn"},
		{"too old", func(r *expense.Record) { r.Date = "2026-03-02" }, []string{"EXPENSE_TOO_OLD"}, "violation"},
		{"fx", func(r *expense.Record) { r.Currency = "EUR"; r.AmountMinor = 999900 }, []string{"FX_REVIEW"}, "warn"},
	}
	for _, c := range cases {
		got := p.Evaluate(rec(c.mod), today)
		if !slices.Equal(rules(got), c.want) || got.Decision != c.dec {
			t.Errorf("%s: rules=%v decision=%s; want %v %s", c.name, rules(got), got.Decision, c.want, c.dec)
		}
	}
}

func TestLoadRejectsBadPolicy(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		f := filepath.Join(dir, "p.yaml")
		_ = os.WriteFile(f, []byte(body), 0o600)
		return f
	}
	for name, body := range map[string]string{
		"no version":   "base_currency: USD\nalcohol: warn\nreview: {min_confidence: 0.5}\n",
		"bad alcohol":  "version: x\nbase_currency: USD\nalcohol: maybe\nreview: {min_confidence: 0.5}\n",
		"unknown key":  "version: x\nbase_currency: USD\nalcohol: warn\nreview: {min_confidence: 0.5}\nbogus: 1\n",
		"bad category": "version: x\nbase_currency: USD\nalcohol: warn\nreview: {min_confidence: 0.5}\ncategory_limits: {food: 5}\n",
		"bad min conf": "version: x\nbase_currency: USD\nalcohol: warn\nreview: {min_confidence: 0}\n",
	} {
		if _, err := Load(write(body)); err == nil {
			t.Errorf("%s: expected error", name)
		} else if strings.Contains(err.Error(), "no such file") {
			t.Errorf("%s: wrong error %v", name, err)
		}
	}
}
