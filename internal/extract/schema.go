// Package extract owns the extraction contract: JSON schema, prompt versions,
// and strict parsing of model output.
package extract

import "github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"

func strEnum(vals []string) map[string]any {
	return map[string]any{"type": "string", "enum": vals}
}

// Schema returns the JSON schema (draft 2020-12 subset accepted by OpenAI
// strict mode and Anthropic tool input_schema). Every property is required.
func Schema() map[string]any {
	conf := map[string]any{}
	req := []string{}
	for _, f := range expense.FieldNames {
		conf[f] = map[string]any{"type": "number", "minimum": 0, "maximum": 1}
		req = append(req, f)
	}
	props := map[string]any{
		"merchant":         map[string]any{"type": "string"},
		"date":             map[string]any{"type": "string", "description": "ISO 8601 YYYY-MM-DD, or empty string if absent"},
		"amount":           map[string]any{"type": "string", "description": "grand total as printed, digits and separators only"},
		"currency":         map[string]any{"type": "string", "description": "ISO 4217 code, uppercase"},
		"category":         strEnum(expense.Categories),
		"tax":              map[string]any{"type": []string{"string", "null"}, "description": "tax amount as printed, or null"},
		"attendees":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"payment_method":   strEnum(expense.PaymentMethods),
		"document_type":    strEnum(expense.DocumentTypes),
		"contains_alcohol": map[string]any{"type": "boolean"},
		"confidence": map[string]any{
			"type": "object", "properties": conf, "required": req, "additionalProperties": false,
		},
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             []string{"merchant", "date", "amount", "currency", "category", "tax", "attendees", "payment_method", "document_type", "contains_alcohol", "confidence"},
		"additionalProperties": false,
	}
}
