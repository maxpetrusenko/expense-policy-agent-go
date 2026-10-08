package extract

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

const goodJSON = `{"merchant":"Cafe Uno","date":"2026-04-02","amount":"18.40","currency":"USD","category":"meals","tax":null,
"attendees":[],"payment_method":"card","document_type":"itemized_receipt","contains_alcohol":false,
"confidence":{"merchant":0.9,"date":0.9,"amount":0.9,"currency":0.9,"category":0.8,"tax":0.9,"attendees":0.9,"payment_method":0.9}}`

func TestParseOK(t *testing.T) {
	ex, err := Parse([]byte("```json\n" + goodJSON + "\n```"))
	if err != nil || ex.Merchant != "Cafe Uno" || ex.Tax != nil {
		t.Fatalf("ex=%+v err=%v", ex, err)
	}
}

func TestParseViolations(t *testing.T) {
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		_ = json.Unmarshal([]byte(goodJSON), &m)
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	cases := map[string][]byte{
		"not json":      []byte("sorry, I cannot"),
		"missing key":   mutate(func(m map[string]any) { delete(m, "amount") }),
		"unknown key":   mutate(func(m map[string]any) { m["extra"] = 1 }),
		"bad enum":      mutate(func(m map[string]any) { m["category"] = "food" }),
		"wrong type":    mutate(func(m map[string]any) { m["amount"] = 18.4 }),
		"conf range":    mutate(func(m map[string]any) { m["confidence"].(map[string]any)["date"] = 1.5 }),
		"conf missing":  mutate(func(m map[string]any) { delete(m["confidence"].(map[string]any), "tax") }),
		"null attendee": mutate(func(m map[string]any) { m["attendees"] = nil }),
	}
	for name, raw := range cases {
		_, err := Parse(raw)
		var fe *failure.Error
		if !errors.As(err, &fe) || fe.Class != failure.Schema {
			t.Errorf("%s: want schema error, got %v", name, err)
		}
	}
}

func TestSchemaShape(t *testing.T) {
	s := Schema()
	if s["additionalProperties"] != false {
		t.Fatal("additionalProperties must be false")
	}
	props := s["properties"].(map[string]any)
	req := s["required"].([]string)
	if len(props) != len(req) {
		t.Fatalf("every property must be required: %d vs %d", len(props), len(req))
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestPromptVersions(t *testing.T) {
	for _, v := range Versions {
		if s, err := System(v); err != nil || s == "" {
			t.Errorf("%s: %v", v, err)
		}
	}
	if _, err := System("v9"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Error("expected unknown version error")
	}
}
