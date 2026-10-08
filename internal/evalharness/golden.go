// Package evalharness runs the pipeline over a golden set and scores it. It
// exists to gate prompt changes: a candidate prompt that scores below the
// thresholds, or below the baseline, fails the build.
package evalharness

import (
	"encoding/json"
	"fmt"
	"os"
)

// Golden is the on-disk golden set.
type Golden struct {
	Description string `json:"description"`
	Now         string `json:"now"` // processing date, YYYY-MM-DD
	Cases       []Case `json:"cases"`
}

// Case is one synthetic receipt with its expected outcome. Cases with
// ExpectFailure set are documents the system must reject with that class.
type Case struct {
	ID            string     `json:"id"`
	Note          string     `json:"note"`
	Text          string     `json:"text"`
	Expected      *Fields    `json:"expected,omitempty"`
	Policy        *PolicyExp `json:"policy,omitempty"`
	NeedsReview   bool       `json:"needs_review"`
	ExpectFailure string     `json:"expect_failure,omitempty"`
}

// Fields are the expected extraction values. Amount and tax are in major
// units as strings so no float comparison is involved.
type Fields struct {
	Merchant      string   `json:"merchant"`
	Date          string   `json:"date"`
	Amount        string   `json:"amount"`
	Currency      string   `json:"currency"`
	Category      string   `json:"category"`
	Tax           *string  `json:"tax"`
	Attendees     []string `json:"attendees"`
	PaymentMethod string   `json:"payment_method"`
}

// PolicyExp is the expected policy outcome.
type PolicyExp struct {
	Decision string   `json:"decision"`
	Rules    []string `json:"rules"`
}

// LoadGolden reads and sanity-checks a golden file.
func LoadGolden(path string) (*Golden, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g Golden
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(g.Cases) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	seen := map[string]bool{}
	for _, c := range g.Cases {
		if c.ID == "" || seen[c.ID] {
			return nil, fmt.Errorf("%s: missing or duplicate case id %q", path, c.ID)
		}
		seen[c.ID] = true
		if (c.ExpectFailure == "") == (c.Expected == nil || c.Policy == nil) {
			return nil, fmt.Errorf("case %s: needs either expect_failure or expected+policy", c.ID)
		}
	}
	return &g, nil
}
