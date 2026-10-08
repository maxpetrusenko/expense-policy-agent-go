package expense

// Category values the extractor may return.
var Categories = []string{"meals", "lodging", "transport", "airfare", "office_supplies", "software", "entertainment", "other"}

// PaymentMethods the extractor may return.
var PaymentMethods = []string{"card", "cash", "other", "unknown"}

// DocumentTypes the extractor may return.
var DocumentTypes = []string{"itemized_receipt", "invoice", "card_slip", "other"}

// FieldNames are the fields that carry a per-field confidence.
var FieldNames = []string{"merchant", "date", "amount", "currency", "category", "tax", "attendees", "payment_method"}

// Extraction is the raw, schema-checked LLM output. Amounts and dates stay as
// printed strings; Go parses them in the validate stage.
type Extraction struct {
	Merchant        string             `json:"merchant"`
	Date            string             `json:"date"`
	Amount          string             `json:"amount"`
	Currency        string             `json:"currency"`
	Category        string             `json:"category"`
	Tax             *string            `json:"tax"`
	Attendees       []string           `json:"attendees"`
	PaymentMethod   string             `json:"payment_method"`
	DocumentType    string             `json:"document_type"`
	ContainsAlcohol bool               `json:"contains_alcohol"`
	Confidence      map[string]float64 `json:"confidence"`
}

// Record is the validated, typed expense proposal. Zero values mean the field
// failed validation (see Issues on the pipeline result).
type Record struct {
	Merchant        string   `json:"merchant"`
	Date            string   `json:"date"` // YYYY-MM-DD or empty
	AmountMinor     int64    `json:"amount_minor"`
	Currency        string   `json:"currency"` // ISO 4217 or empty
	Category        string   `json:"category"`
	TaxMinor        *int64   `json:"tax_minor"`
	Attendees       []string `json:"attendees"`
	PaymentMethod   string   `json:"payment_method"`
	DocumentType    string   `json:"document_type"`
	ContainsAlcohol bool     `json:"contains_alcohol"`
}

// Issue is a deterministic validation problem on one field.
type Issue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Finding is one policy rule outcome.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // warn | violation
	Message  string `json:"message"`
}

// Decisions, ordered by severity.
const (
	DecisionPass      = "pass"
	DecisionWarn      = "warn"
	DecisionViolation = "violation"
)
