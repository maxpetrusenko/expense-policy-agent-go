package extract

import "fmt"

// Versions lists the prompt versions the pipeline can run.
var Versions = []string{"v1", "v2"}

const systemV1 = `You extract expense fields from receipt text. Return the fields in the required JSON format. Report a confidence between 0 and 1 for each field.`

const systemV2 = `You extract expense fields from messy receipt or invoice text for an expense-reporting system. A deterministic policy engine decides compliance after you; you only read the document.

Rules:
- amount is the grand total actually charged, not a subtotal, tip suggestion, or balance. Copy it as printed.
- tax is the tax or VAT amount if a line shows one, otherwise null. Never infer it.
- date is the transaction date in ISO 8601. If day and month are ambiguous, use month/day (US) and lower the date confidence below 0.6.
- currency is an ISO 4217 code. Infer from symbols or country only when the document supports it.
- attendees are named people listed as guests or party members. Empty list if none are named.
- contains_alcohol is true only if a line item is an alcoholic drink.
- document_type is card_slip for a bare card terminal slip with no line items, invoice for an invoice, itemized_receipt for a receipt with line items.
- Confidence is your honest probability the field is exactly right. Use values below 0.6 for guesses. Do not default to 0.9.
- Never invent values. Use an empty string or null when the document does not say.`

// System returns the system prompt for a version.
func System(version string) (string, error) {
	switch version {
	case "v1":
		return systemV1, nil
	case "v2":
		return systemV2, nil
	}
	return "", fmt.Errorf("unknown prompt version %q (known: %v)", version, Versions)
}

// User wraps the document text for the user turn.
func User(text string) string {
	return "Extract the expense fields from this document.\n\n<document>\n" + text + "\n</document>"
}
