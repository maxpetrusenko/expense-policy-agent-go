// Package provider defines the model interface and its implementations:
// Anthropic (official SDK), OpenAI-compatible (official SDK, configurable
// base URL), and a deterministic mock used by tests, CI and the default eval.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Request is everything a provider needs for one extraction call.
type Request struct {
	System        string
	User          string
	Text          string         // raw document text (the mock reads this)
	ImagePath     string         // optional; sent only by vision-capable providers
	PromptVersion string         // lets the mock emulate per-version behaviour
	Schema        map[string]any // JSON schema the output must satisfy
}

// Provider returns the raw JSON the model produced. Parsing and schema
// enforcement happen in the pipeline so they apply to every provider equally.
type Provider interface {
	Name() string
	Extract(ctx context.Context, req Request) ([]byte, error)
}

// Config selects and configures a provider.
type Config struct {
	Name    string // mock | anthropic | openai
	Model   string
	BaseURL string // openai-compatible endpoint, e.g. http://localhost:11434/v1
}

// FromEnv builds a Config from flags' values falling back to environment.
func FromEnv(name, model, baseURL string) Config {
	c := Config{Name: name, Model: model, BaseURL: baseURL}
	if c.Name == "" {
		c.Name = envOr("EXPENSE_PROVIDER", "mock")
	}
	if c.Model == "" {
		c.Model = os.Getenv("EXPENSE_MODEL")
	}
	if c.BaseURL == "" {
		c.BaseURL = os.Getenv("OPENAI_BASE_URL")
	}
	return c
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// New constructs the provider. Keys come from ANTHROPIC_API_KEY and
// OPENAI_API_KEY; neither is needed for the mock.
func New(c Config) (Provider, error) {
	switch strings.ToLower(c.Name) {
	case "mock":
		return Mock{}, nil
	case "anthropic":
		return NewAnthropic(c.Model, "", ""), nil
	case "openai", "openai-compatible":
		return NewOpenAI(c.Model, c.BaseURL, ""), nil
	}
	return nil, fmt.Errorf("unknown provider %q (mock, anthropic, openai)", c.Name)
}

// FailFirst wraps a provider and returns err for the first n calls. Tests use
// it to exercise retry, classification and DLQ behaviour.
type FailFirst struct {
	Inner Provider
	N     int
	Err   error
	calls int
}

func (f *FailFirst) Name() string { return f.Inner.Name() }

func (f *FailFirst) Extract(ctx context.Context, r Request) ([]byte, error) {
	f.calls++
	if f.calls <= f.N {
		return nil, f.Err
	}
	return f.Inner.Extract(ctx, r)
}

// Calls reports how many times Extract was invoked.
func (f *FailFirst) Calls() int { return f.calls }
