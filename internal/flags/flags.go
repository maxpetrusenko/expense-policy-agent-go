// Package flags selects the extraction prompt version per document so a new
// prompt can be rolled out to a fixed percentage of traffic and rolled back by
// changing one environment variable.
package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/extract"
)

// Environment variable names.
const (
	EnvStable    = "EXPENSE_PROMPT_VERSION"
	EnvCandidate = "EXPENSE_PROMPT_CANDIDATE"
	EnvPercent   = "EXPENSE_PROMPT_CANDIDATE_PERCENT"
)

// Flags is the resolved rollout configuration.
type Flags struct {
	Stable    string
	Candidate string // empty means no rollout in progress
	Percent   int    // 0..100 share of documents routed to Candidate
}

// FromEnv reads the flags. Defaults: stable v2, no candidate.
func FromEnv() (Flags, error) {
	f := Flags{Stable: "v2", Candidate: os.Getenv(EnvCandidate)}
	if v := os.Getenv(EnvStable); v != "" {
		f.Stable = v
	}
	if v := os.Getenv(EnvPercent); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return f, fmt.Errorf("%s=%q: %w", EnvPercent, v, err)
		}
		f.Percent = n
	}
	return f, f.Validate()
}

// Validate checks versions exist and the percentage is in range.
func (f Flags) Validate() error {
	if !slices.Contains(extract.Versions, f.Stable) {
		return fmt.Errorf("stable prompt version %q unknown (known: %v)", f.Stable, extract.Versions)
	}
	if f.Candidate != "" && !slices.Contains(extract.Versions, f.Candidate) {
		return fmt.Errorf("candidate prompt version %q unknown (known: %v)", f.Candidate, extract.Versions)
	}
	if f.Percent < 0 || f.Percent > 100 {
		return fmt.Errorf("candidate percent %d outside 0..100", f.Percent)
	}
	return nil
}

// Resolve picks the prompt version for a document. Bucketing hashes the
// document key, so the same document always gets the same version at a given
// percentage and raising the percentage only adds documents to the candidate.
func (f Flags) Resolve(docKey string) string {
	if f.Candidate == "" || f.Percent <= 0 {
		return f.Stable
	}
	sum := sha256.Sum256([]byte(docKey))
	if int(binary.BigEndian.Uint32(sum[:4])%100) < f.Percent {
		return f.Candidate
	}
	return f.Stable
}
