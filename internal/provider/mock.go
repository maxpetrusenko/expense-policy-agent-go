package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

// Test hooks: a document containing one of these markers makes the mock fail
// in a specific way, so the CLI and tests can demonstrate failure handling
// without a network.
const (
	HookSchema  = "<<MOCK:schema-violation>>"
	HookTimeout = "<<MOCK:timeout>>"
	HookStatus  = "<<MOCK:http-429>>"
)

// Mock is a deterministic rule-based extractor. It stands in for an LLM so
// the pipeline, policy engine and eval harness run without credentials.
// Prompt version "v1" deliberately has the weaknesses a first-draft prompt
// tends to have (picks subtotals, ignores tax and attendees, overconfident);
// "v2" fixes them. That gap is what the eval gate is exercised against. It
// measures the harness, not model quality.
type Mock struct{}

func (Mock) Name() string { return "mock" }

func (Mock) Extract(_ context.Context, r Request) ([]byte, error) {
	switch {
	case strings.Contains(r.Text, HookTimeout):
		return nil, context.DeadlineExceeded
	case strings.Contains(r.Text, HookStatus):
		return nil, &failure.StatusError{Status: 429, Err: errors.New("mock rate limit")}
	case strings.Contains(r.Text, HookSchema):
		return []byte(`{"merchant":"Broken","category":"food"}`), nil
	}
	return json.Marshal(mockExtract(r.Text, r.PromptVersion != "v1"))
}

func mockExtract(text string, v2 bool) expense.Extraction {
	ex := expense.Extraction{Attendees: []string{}, Confidence: map[string]float64{}}
	c := ex.Confidence

	ex.Merchant, c["merchant"] = findMerchant(text)
	ex.Date, c["date"] = findDate(text, v2)
	ex.Amount, c["amount"] = findAmount(text, v2)
	ex.Currency, c["currency"] = findCurrency(text, v2)
	ex.Category, c["category"] = findCategory(text, v2)
	c["tax"], c["attendees"] = 0.8, 0.8
	if v2 {
		if t, ok := findTax(text); ok {
			ex.Tax, c["tax"] = &t, 0.9
		}
		if a := findAttendees(text); len(a) > 0 {
			ex.Attendees, c["attendees"] = a, 0.9
		}
	}
	ex.PaymentMethod, c["payment_method"] = findPayment(text)
	ex.DocumentType = "itemized_receipt"
	if v2 {
		ex.DocumentType = findDocType(text)
	}
	ex.ContainsAlcohol = hasAlcohol(text, v2)
	return ex
}
