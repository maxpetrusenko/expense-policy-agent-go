// Package store holds the two pieces of durable state: the idempotency store
// (key -> finished result) and the dead-letter directory.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Key is the idempotency key: sha256 over the document bytes and the policy
// version, so editing the document or the policy produces a new key.
func Key(doc []byte, policyVersion string) string {
	h := sha256.New()
	h.Write(doc)
	h.Write([]byte{0})
	h.Write([]byte(policyVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// Idempotency is a JSON-file map of key -> raw result. Safe for concurrent
// use within one process; writes are atomic (temp file + rename).
type Idempotency struct {
	path string
	mu   sync.Mutex
	data map[string]json.RawMessage
}

// OpenIdempotency loads (or creates) the store at path.
func OpenIdempotency(path string) (*Idempotency, error) {
	s := &Idempotency{path: path, data: map[string]json.RawMessage{}}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("idempotency store %s is corrupt: %w", path, err)
		}
	}
	return s, nil
}

// Get returns the stored result for key.
func (s *Idempotency) Get(key string) (json.RawMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// Put records a finished result and flushes to disk.
func (s *Idempotency) Put(key string, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = raw
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Len is the number of stored keys.
func (s *Idempotency) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

// DeadLetter is one failed document with the reason it failed.
type DeadLetter struct {
	Key           string    `json:"key"`
	Source        string    `json:"source"`
	Class         string    `json:"class"`
	Stage         string    `json:"stage"`
	Error         string    `json:"error"`
	Attempts      int       `json:"attempts"`
	PromptVersion string    `json:"prompt_version"`
	PolicyVersion string    `json:"policy_version"`
	At            time.Time `json:"at"`
	// RawOutput is the model output for schema violations, kept for debugging.
	RawOutput string `json:"raw_output,omitempty"`
	Document  string `json:"document"`
}

// DLQ writes one JSON file per dead-lettered document, named by key.
type DLQ struct{ Dir string }

// Write stores d in the DLQ and returns the file path.
func (q DLQ) Write(d DeadLetter) (string, error) {
	if err := os.MkdirAll(q.Dir, 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(q.Dir, d.Key+".json")
	return p, os.WriteFile(p, b, 0o600)
}

// List returns every dead letter in the directory.
func (q DLQ) List() ([]DeadLetter, error) {
	files, err := filepath.Glob(filepath.Join(q.Dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []DeadLetter
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var d DeadLetter
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, d)
	}
	return out, nil
}
