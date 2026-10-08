package validate

import (
	"errors"
	"testing"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

var now = time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

func base() expense.Extraction {
	return expense.Extraction{Merchant: "Cafe Uno", Date: "2026-06-01", Amount: "$1,018.40", Currency: "usd", Category: "meals", Attendees: []string{" Ana ", ""}}
}

func codes(is []expense.Issue) map[string]string {
	m := map[string]string{}
	for _, i := range is {
		m[i.Field] = i.Code
	}
	return m
}

func TestHappyPath(t *testing.T) {
	rec, issues, err := Run(base(), now)
	if err != nil || len(issues) != 0 {
		t.Fatalf("err=%v issues=%v", err, issues)
	}
	if rec.AmountMinor != 101840 || rec.Currency != "USD" || rec.Date != "2026-06-01" || len(rec.Attendees) != 1 || rec.Attendees[0] != "Ana" {
		t.Fatalf("rec=%+v", rec)
	}
}

func TestSoftIssues(t *testing.T) {
	ex := base()
	ex.Merchant, ex.Currency, ex.Date = " ", "XXX", "2026-07-15"
	bad := "5000.00"
	ex.Tax = &bad
	_, issues, err := Run(ex, now)
	if err != nil {
		t.Fatal(err)
	}
	got := codes(issues)
	want := map[string]string{"merchant": "missing", "currency": "invalid_code", "date": "future", "tax": "out_of_range"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	ex.Date = "2024-01-01"
	_, issues, _ = Run(ex, now)
	if codes(issues)["date"] != "too_old" {
		t.Error("expected too_old")
	}
	ex.Date = "03/04/2026"
	_, issues, _ = Run(ex, now)
	if codes(issues)["date"] != "unparseable" {
		t.Error("expected unparseable")
	}
}

func TestHardFailure(t *testing.T) {
	for _, amt := range []string{"", "n/a", "0.00", "-4.00"} {
		ex := base()
		ex.Amount = amt
		_, _, err := Run(ex, now)
		var fe *failure.Error
		if !errors.As(err, &fe) || fe.Class != failure.InvalidDoc {
			t.Errorf("%q: want invalid_document, got %v", amt, err)
		}
	}
}
