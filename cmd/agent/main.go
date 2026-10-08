// Command agent proposes expense records from receipt files. For each input it
// prints one JSON result per line on stdout; logs and the run summary go to
// stderr. Exit code 1 means at least one document was dead-lettered.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/flags"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/pipeline"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/policy"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/provider"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/retry"
	"github.com/maxpetrusenko/expense-policy-agent-go/internal/store"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		polPath  = fs.String("policy", "policies/default.yaml", "policy YAML")
		provName = fs.String("provider", "", "mock (default), anthropic, openai; or EXPENSE_PROVIDER")
		model    = fs.String("model", "", "model name; or EXPENSE_MODEL")
		baseURL  = fs.String("base-url", "", "OpenAI-compatible base URL (for example http://localhost:11434/v1); or OPENAI_BASE_URL")
		stateDir = fs.String("state", ".expense-state", "directory for the idempotency store and dead-letter queue")
		force    = fs.Bool("force", false, "reprocess documents already in the idempotency store")
		attempts = fs.Int("max-attempts", 4, "model call attempts for retryable failures")
		nowStr   = fs.String("now", "", "processing date YYYY-MM-DD (default: today)")
		logLevel = fs.String("log-level", "info", "debug, info, warn, error")
		listDLQ  = fs.Bool("dlq-list", false, "list dead-lettered documents and exit")
	)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: agent [flags] <receipt file | directory>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dlq := &store.DLQ{Dir: filepath.Join(*stateDir, "dlq")}
	if *listDLQ {
		return printDLQ(dlq, stdout, stderr)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintln(stderr, "error: bad -log-level:", err)
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: lvl}))

	pol, err := policy.Load(*polPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	fl, err := flags.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	prov, err := provider.New(provider.FromEnv(*provName, *model, *baseURL))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	idem, err := store.OpenIdempotency(filepath.Join(*stateDir, "idempotency.json"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	now := time.Now
	if *nowStr != "" {
		t, err := time.Parse("2006-01-02", *nowStr)
		if err != nil {
			fmt.Fprintln(stderr, "error: bad -now:", err)
			return 2
		}
		now = func() time.Time { return t }
	}
	rp := retry.Default()
	rp.MaxAttempts = *attempts

	docs, err := collect(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	p := &pipeline.Pipeline{Provider: prov, Policy: pol, Flags: fl, Retry: rp, Idem: idem, DLQ: dlq, Now: now, Log: log, Force: *force}

	var ok, cached, review, dead int
	enc := json.NewEncoder(stdout)
	for _, d := range docs {
		res, err := p.Process(context.Background(), d)
		switch {
		case err != nil:
			dead++
		case res.Cached:
			cached++
			_ = enc.Encode(res)
		default:
			ok++
			if res.NeedsReview {
				review++
			}
			_ = enc.Encode(res)
		}
	}
	fmt.Fprintf(stderr, "done: %d documents, %d processed (%d need review), %d skipped as already processed, %d dead-lettered (%s)\n",
		len(docs), ok, review, cached, dead, dlq.Dir)
	if dead > 0 {
		return 1
	}
	return 0
}

func printDLQ(q *store.DLQ, stdout, stderr io.Writer) int {
	items, err := q.List()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	for _, d := range items {
		fmt.Fprintf(stdout, "%s  %-18s stage=%-8s attempts=%d  %s\n", d.Key[:12], d.Class, d.Stage, d.Attempts, d.Source)
		fmt.Fprintf(stdout, "    %s\n", d.Error)
	}
	fmt.Fprintf(stderr, "%d dead letters in %s\n", len(items), q.Dir)
	return 0
}
