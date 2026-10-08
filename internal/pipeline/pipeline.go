// Package pipeline wires the stages: idempotency check, prompt selection,
// model extraction with retry, deterministic validation, policy evaluation,
// and review routing. The model proposes; this package and policy decide.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/extract"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/flags"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/policy"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/provider"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/retry"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/store"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/validate"
)

// Document is one input: receipt text, optionally with an image for vision
// providers.
type Document struct {
	Source    string
	Text      string
	ImagePath string
}

// Result is the proposed expense plus everything a reviewer needs.
type Result struct {
	Source        string             `json:"source"`
	Key           string             `json:"key"`
	Provider      string             `json:"provider"`
	PromptVersion string             `json:"prompt_version"`
	PolicyVersion string             `json:"policy_version"`
	Record        expense.Record     `json:"record"`
	Confidence    map[string]float64 `json:"confidence"`
	Issues        []expense.Issue    `json:"issues"`
	Findings      []expense.Finding  `json:"findings"`
	Decision      string             `json:"decision"`
	NeedsReview   bool               `json:"needs_review"`
	ReviewReasons []string           `json:"review_reasons"`
	Attempts      int                `json:"attempts"`
	TimingsMs     map[string]float64 `json:"timings_ms"`
	Cached        bool               `json:"cached,omitempty"`
}

// Pipeline holds the dependencies. Idem and DLQ may be nil (the eval harness
// runs without durable state).
type Pipeline struct {
	Provider provider.Provider
	Policy   *policy.Policy
	Flags    flags.Flags
	Retry    retry.Policy
	Idem     *store.Idempotency
	DLQ      *store.DLQ
	Now      func() time.Time
	Log      *slog.Logger
	// Force reprocesses documents even when an idempotency record exists, for
	// example after changing the prompt version.
	Force bool
}

func (p *Pipeline) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Pipeline) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Process runs one document end to end. On failure the document is written to
// the DLQ (when configured) and the classified error is returned.
func (p *Pipeline) Process(ctx context.Context, doc Document) (*Result, error) {
	start := time.Now()
	key, err := p.key(doc)
	if err != nil {
		return nil, p.deadLetter(doc, "", "", nil, 0, failure.New(failure.InvalidDoc, "input", err))
	}
	log := p.log().With("source", doc.Source, "key", key[:12])

	if p.Idem != nil && !p.Force {
		if raw, ok := p.Idem.Get(key); ok {
			var r Result
			if err := json.Unmarshal(raw, &r); err == nil {
				r.Cached = true
				log.Info("skip: already processed", "decision", r.Decision)
				return &r, nil
			}
			log.Warn("unreadable idempotency record, reprocessing")
		}
	}

	version := p.Flags.Resolve(key)
	res := &Result{Source: doc.Source, Key: key, Provider: p.Provider.Name(), PromptVersion: version,
		PolicyVersion: p.Policy.Version, TimingsMs: map[string]float64{}}
	log = log.With("prompt", version)

	if strings.TrimSpace(doc.Text) == "" && doc.ImagePath == "" {
		return nil, p.deadLetter(doc, key, version, nil, 0, failure.New(failure.InvalidDoc, "input", errors.New("document has no text and no image")))
	}

	// Stage 1: model extraction, retried on transient failures only.
	t := time.Now()
	var raw []byte
	var ex expense.Extraction
	attempts, err := retry.Do(ctx, p.Retry, func(ctx context.Context, attempt int) error {
		var e error
		if raw, e = p.callModel(ctx, doc, version); e != nil {
			log.Warn("model call failed", "attempt", attempt, "class", failure.Classify(e), "err", e)
			return e
		}
		ex, e = extract.Parse(raw)
		return e
	})
	res.Attempts = attempts
	res.TimingsMs["extract"] = ms(time.Since(t))
	if err != nil {
		return nil, p.deadLetter(doc, key, version, raw, attempts, err)
	}

	// Stage 2: deterministic validation.
	t = time.Now()
	rec, issues, err := validate.Run(ex, p.now())
	res.TimingsMs["validate"] = ms(time.Since(t))
	if err != nil {
		return nil, p.deadLetter(doc, key, version, raw, attempts, err)
	}

	// Stage 3: policy. Code decides.
	t = time.Now()
	pr := p.Policy.Evaluate(rec, p.now().Format("2006-01-02"))
	res.TimingsMs["policy"] = ms(time.Since(t))

	res.Record, res.Confidence, res.Issues = rec, ex.Confidence, issues
	res.Findings, res.Decision = pr.Findings, pr.Decision
	if res.Issues == nil {
		res.Issues = []expense.Issue{}
	}
	if res.Findings == nil {
		res.Findings = []expense.Finding{}
	}
	res.ReviewReasons = p.reviewReasons(res)
	if res.ReviewReasons == nil {
		res.ReviewReasons = []string{}
	}
	res.NeedsReview = len(res.ReviewReasons) > 0
	res.TimingsMs["total"] = ms(time.Since(start))

	if p.Idem != nil {
		if err := p.Idem.Put(key, res); err != nil {
			log.Error("idempotency write failed", "err", err)
		}
	}
	log.Info("processed", "decision", res.Decision, "needs_review", res.NeedsReview, "attempts", attempts,
		"extract_ms", res.TimingsMs["extract"], "total_ms", res.TimingsMs["total"])
	return res, nil
}

func (p *Pipeline) key(doc Document) (string, error) {
	b := []byte(doc.Text)
	if doc.ImagePath != "" {
		img, err := os.ReadFile(doc.ImagePath)
		if err != nil {
			return "", err
		}
		b = append(b, img...)
	}
	return store.Key(b, p.Policy.Version), nil
}

func (p *Pipeline) callModel(ctx context.Context, doc Document, version string) ([]byte, error) {
	sys, err := extract.System(version)
	if err != nil {
		return nil, failure.New(failure.Permanent, "prompt", err)
	}
	return p.Provider.Extract(ctx, provider.Request{
		System: sys, User: extract.User(doc.Text), Text: doc.Text, ImagePath: doc.ImagePath,
		PromptVersion: version, Schema: extract.Schema(),
	})
}

func (p *Pipeline) reviewReasons(r *Result) []string {
	var out []string
	for _, f := range expense.FieldNames {
		if c, ok := r.Confidence[f]; ok && c < p.Policy.Review.MinConfidence {
			out = append(out, "low_confidence:"+f)
		}
	}
	for _, i := range r.Issues {
		out = append(out, "validation:"+i.Field+":"+i.Code)
	}
	if r.Decision != expense.DecisionPass {
		out = append(out, "policy:"+r.Decision)
	}
	slices.Sort(out)
	return out
}

func (p *Pipeline) deadLetter(doc Document, key, version string, raw []byte, attempts int, err error) error {
	class := failure.Classify(err)
	stage := "extract"
	var fe *failure.Error
	if errors.As(err, &fe) {
		stage = fe.Stage
	}
	p.log().Error("dead-lettered", "source", doc.Source, "class", class, "stage", stage, "attempts", attempts, "err", err)
	if p.DLQ == nil {
		return err
	}
	if key == "" {
		key = store.Key([]byte(doc.Source+doc.Text), p.Policy.Version)
	}
	d := store.DeadLetter{Key: key, Source: doc.Source, Class: string(class), Stage: stage, Error: err.Error(),
		Attempts: attempts, PromptVersion: version, PolicyVersion: p.Policy.Version, At: p.now().UTC(), Document: doc.Text}
	if class == failure.Schema {
		d.RawOutput = string(raw)
	}
	if _, werr := p.DLQ.Write(d); werr != nil {
		p.log().Error("dlq write failed", "err", werr)
	}
	return err
}
