package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

// Parse strictly decodes raw model output into an Extraction. Any violation
// of the schema returns a failure.Schema error (non-retryable by design: the
// same prompt will usually fail the same way, so the document is dead-lettered
// with the raw output for inspection).
func Parse(raw []byte) (expense.Extraction, error) {
	var ex expense.Extraction
	body := trimToObject(raw)
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return ex, schemaErr("not a JSON object: %v", err)
	}
	for _, k := range requiredKeys() {
		if _, ok := keys[k]; !ok {
			return ex, schemaErr("missing required field %q", k)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ex); err != nil {
		return ex, schemaErr("decode: %v", err)
	}
	if err := check(ex); err != nil {
		return ex, err
	}
	return ex, nil
}

func requiredKeys() []string {
	return []string{"merchant", "date", "amount", "currency", "category", "tax", "attendees", "payment_method", "document_type", "contains_alcohol", "confidence"}
}

func check(ex expense.Extraction) error {
	if !slices.Contains(expense.Categories, ex.Category) {
		return schemaErr("category %q not in enum", ex.Category)
	}
	if !slices.Contains(expense.PaymentMethods, ex.PaymentMethod) {
		return schemaErr("payment_method %q not in enum", ex.PaymentMethod)
	}
	if !slices.Contains(expense.DocumentTypes, ex.DocumentType) {
		return schemaErr("document_type %q not in enum", ex.DocumentType)
	}
	if ex.Attendees == nil {
		return schemaErr("attendees must be an array")
	}
	for _, f := range expense.FieldNames {
		c, ok := ex.Confidence[f]
		if !ok {
			return schemaErr("confidence missing %q", f)
		}
		if c < 0 || c > 1 {
			return schemaErr("confidence[%s]=%v outside [0,1]", f, c)
		}
	}
	return nil
}

// trimToObject tolerates code fences or chatter around the JSON object, which
// some OpenAI-compatible local servers emit even when asked for JSON only.
func trimToObject(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	i, j := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return []byte(s)
	}
	return []byte(s[i : j+1])
}

func schemaErr(format string, a ...any) error {
	return failure.New(failure.Schema, "extract", fmt.Errorf(format, a...))
}
