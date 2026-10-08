# expense-policy-agent-go

A Go pipeline that reads messy receipt text (or an image), asks an LLM to extract expense fields into a fixed JSON schema, validates them in plain Go, and checks the result against an expense policy written in YAML. The output is a proposed expense record, the policy findings, a confidence per field, and a flag saying whether a person should look at it before it is submitted. It ships with an offline eval harness that scores prompt versions against a golden set and exits non-zero when a prompt change makes things worse.

The model reads documents. Go code decides what the policy says about them.

This is a reference implementation of the agent plus eval plus reliability shape, written to be read and run. It is not a product, and the policy, merchants and receipts in it are synthetic.

## Architecture

```
 receipt text / image
        |
        v
 +------------------+   key = sha256(document, policy version)
 | idempotency      |-- seen? --> return stored result (skip, --force to override)
 +------------------+
        | new
        v
 +------------------+   feature flag: stable prompt, optional candidate at N%
 | prompt selection |   (bucket = hash of the idempotency key)
 +------------------+
        |
        v
 +------------------+   Provider interface:
 | extract (LLM)    |   anthropic | openai-compatible | mock
 | retry + backoff  |   JSON-schema-constrained output
 +------------------+   retryable: timeout, 429, 5xx.  not retried: schema, 4xx
        |
        v
 +------------------+   strict decode: required keys, enums, confidence in [0,1]
 | schema check     |   violation -> dead letter (with the raw model output)
 +------------------+
        |
        v
 +------------------+   amount parse (no floats), ISO date, not future, not too old,
 | validate (Go)    |   ISO 4217 currency, tax < total
 +------------------+   unusable amount -> dead letter; other problems -> issues
        |
        v
 +------------------+   category limits, per-diem, receipt threshold, attendees,
 | policy (Go, YAML)|   weekend, alcohol, FX, expense age
 +------------------+
        |
        v
 +------------------+   low field confidence, validation issues, or any
 | review routing   |   policy warning or violation  ->  needs_review
 +------------------+
        |
        v
 result JSON (stdout)  +  stored for idempotency  +  per-stage timings  +  slog lines (stderr)

 any stage failing for good  ->  dead-letter directory, one JSON file per document
```

## Where the LLM decides and where code decides

| Decision | Owner |
| --- | --- |
| Which text on the page is the merchant, date, total, tax, attendees | LLM |
| Which category the purchase falls in, whether a line is an alcoholic drink | LLM |
| How sure it is about each field | LLM (reported), code (thresholded) |
| Whether "1.234,50" is 1234.50 or 1.23 | Go (`expense.ParseMoney`, integer cents) |
| Whether the date is real, not in the future, not stale | Go (`validate`) |
| Whether the currency is a valid ISO 4217 code | Go (`validate`) |
| Whether the expense is over a limit, per diem, needs a receipt, needs attendees | Go (`policy`, driven by YAML) |
| Whether to retry, give up, or dead-letter | Go (`failure`, `retry`) |
| Whether a human must review | Go (`pipeline.reviewReasons`) |
| Which prompt version a document gets | Go (`flags`) |

The model never sees the policy and cannot approve anything. A wrong extraction can still produce a wrong policy outcome, which is what the confidence thresholds and the eval harness are there to catch.

## Run it

Needs Go 1.25 or newer. No API key is required to build, test, or run the evals.

```
go build ./...
go vet ./...
go test ./...

# process one file or a directory (default provider: mock)
go run ./cmd/agent examples/receipts

# score prompts v1 and v2 side by side; gate on v2 (see below)
go run ./cmd/eval
```

Real providers:

```
# Anthropic (official SDK, forced tool use with the extraction schema as input_schema)
export ANTHROPIC_API_KEY=...
go run ./cmd/agent -provider anthropic examples/receipts            # default model claude-sonnet-5-5
go run ./cmd/agent -provider anthropic -model <model id> examples/receipts

# any OpenAI-compatible endpoint (official SDK, response_format json_schema, strict)
export OPENAI_API_KEY=...
go run ./cmd/agent -provider openai -model gpt-4.1-mini examples/receipts

# local Ollama, no key
go run ./cmd/agent -provider openai -base-url http://localhost:11434/v1 -model llama3.2-vision examples/receipts
```

Environment variables: `EXPENSE_PROVIDER`, `EXPENSE_MODEL`, `OPENAI_BASE_URL`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`. Nothing is read from files and no key is stored in the repo.

In a directory, `name.txt` and `name.png` (or `.jpg`, `.jpeg`, `.webp`, `.gif`) are one document. An image with no text file is image-only. Only the vision-capable providers send the image; the mock ignores it.

`cmd/agent` prints one JSON result per line on stdout and logs to stderr. Exit code 1 means at least one document was dead-lettered. `-dlq-list` prints the dead-letter queue. State (idempotency store and DLQ) lives in `-state`, default `.expense-state/`.

## Reliability

**Idempotency.** The key is `sha256(document bytes, policy version)`. A document already processed under the same policy version returns its stored result without calling the model. Changing the document or the policy version changes the key. The store is a JSON file written atomically (temp file and rename). The key deliberately does not include the prompt version, per the original spec; after changing a prompt, pass `-force` to reprocess. Failed documents are never recorded as done, so they are retried on the next run.

**Failure classification** (`internal/failure`). Classification is by type and HTTP status, never by matching error text.

| Class | Examples | Retried |
| --- | --- | --- |
| `retryable` | context deadline, network timeout, 408, 409, 425, 429, any 5xx | yes |
| `schema_violation` | invalid JSON, missing key, value outside an enum, confidence outside [0,1] | no |
| `invalid_document` | empty input, unreadable image, no parseable positive total | no |
| `permanent` | 400, 401, 403, 404, unknown errors | no |

Unknown errors are `permanent` on purpose, so a bug cannot turn into an endless retry loop.

**Retry.** Up to 4 attempts, 200 ms base, 5 s cap, full jitter (uniform between zero and the exponential ceiling). The SDKs' own retries are switched off so there is one retry policy, and it is the one that is tested. Sleep and the random source are injectable, so the backoff test is deterministic.

**Dead-letter directory.** Each failed document becomes `<state>/dlq/<key>.json` with the class, stage, error, attempt count, prompt and policy versions, the original document text, and for schema violations the raw model output.

**Feature flags.** `EXPENSE_PROMPT_VERSION` sets the stable prompt (default `v2`). `EXPENSE_PROMPT_CANDIDATE` and `EXPENSE_PROMPT_CANDIDATE_PERCENT` route a share of documents to a candidate. Bucketing hashes the idempotency key, so a document always gets the same version at a given percentage and raising the percentage only adds documents to the candidate. Rolling back is setting the percentage to 0.

**Observability.** `log/slog` text logs with source, key prefix, prompt version, attempt, class, decision. Each result carries per-stage timings in milliseconds (`extract`, `validate`, `policy`, `total`).

## Eval harness

`cmd/eval` runs the whole pipeline over `eval/golden.json`: 26 synthetic receipts (24 with expected fields and policy outcomes, 2 that must be rejected). It never calls the network unless you pass `-provider anthropic|openai`.

Metrics: field-level accuracy (overall and per field, amounts compared in integer cents), policy decision accuracy, exact match of the set of triggered rules, review-flag precision and recall, failure classification accuracy, and latency p50 and p95.

`-prompts v1,v2` scores both versions side by side. The last one is the candidate and the first is the baseline. The candidate must meet every threshold and must not fall below the baseline by more than `-max-regression` (default 0). Exit 0 means it passed, 1 means the gate failed, 2 means bad usage or config. CI runs it, and also runs it with the versions swapped to prove the gate rejects a worse prompt.

Defaults: field accuracy 0.93, policy decision accuracy 0.90, review recall 0.95, review precision 0.90, failure classification 1.0, p95 latency 5 s. They sit just under what the current v2 scores, so a real regression trips them. Override with `-min-field-acc` and the other `-min-*` flags.

Real output from `go run ./cmd/eval` (mock provider):

```
golden set: 26 cases (24 scored, 2 expected rejections), now=2026-06-30
provider:   mock

metric                           prompt v1   prompt v2     delta
----------------------------------------------------------------
field accuracy                       79.2%       99.0%    +19.8p
  merchant                          100.0%      100.0%     +0.0p
  date                               79.2%       95.8%    +16.7p
  amount                             70.8%      100.0%    +29.2p
  currency                           87.5%      100.0%    +12.5p
  category                           66.7%       95.8%    +29.2p
  tax                                45.8%      100.0%    +54.2p
  attendees                          83.3%      100.0%    +16.7p
  payment_method                    100.0%      100.0%     +0.0p
policy decision accuracy             66.7%       91.7%    +25.0p
policy rule-set exact match          45.8%       91.7%    +45.8p
review flag precision                88.9%      100.0%    +11.1p
review flag recall                  100.0%      100.0%     +0.0p
failure classification              100.0%      100.0%     +0.0p
latency p50                           83us       142us
latency p95                          198us       255us

prompt v2: 2 of 26 cases with at least one miss
  r19: date got 2026-04-05 want 2026-05-04; policy got warn[WEEKEND] want pass[]
  r20: category got other want meals; policy got pass[] want violation[ATTENDEES_REQUIRED]

GATE PASSED for prompt v2
```

With the versions swapped (`go run ./cmd/eval -prompts v2,v1`) the gate fails and the command exits 1:

```
GATE FAILED for prompt v1:
  - field accuracy 0.792 < required 0.930
  - policy decision accuracy 0.667 < required 0.900
  - review precision 0.889 < required 0.900
  - field accuracy vs baseline v2 0.792 < required 0.990
  - policy decision accuracy vs baseline v2 0.667 < required 0.917
```

### What the mock numbers mean

The mock provider is a rule-based extractor (regexes and keyword lists), not a model. Prompt "v1" is written to behave like a first-draft prompt: it reads the subtotal instead of the total, misses tax and attendees, only recognises `$`, and reports high confidence on guesses. "v2" fixes those. The gap between them exercises the gate. The scores measure the pipeline, the policy engine and the harness. They say nothing about how well any LLM does on receipts. To get that number, run the same command with a real provider, and expect it to be lower and noisier than 99%.

Two golden cases are deliberately beyond the mock's reach so the report is not all green: r19 (a `04/05/2026` date that is really 4 May) and r20 (a receipt with no total label whose category needs context). In both the review flag still fires, which is the behaviour to want: wrong extraction, correct routing to a person.

The golden set is synthetic. Merchants, people, card digits and amounts are invented.

## Layout

```
cmd/agent             CLI: file or directory in, JSON lines out
cmd/eval              eval CLI and gate
internal/expense      domain types, integer-cents money parsing
internal/extract      JSON schema, prompt versions, strict output parsing
internal/provider     Provider interface; anthropic, openai-compatible, mock, FailFirst test wrapper
internal/validate     deterministic field checks
internal/policy       YAML policy loader and rule engine
internal/failure      error classes and classification
internal/retry        bounded retry, backoff, jitter
internal/store        idempotency store and dead-letter queue
internal/flags        prompt version flag and percentage rollout
internal/pipeline     stage orchestration, review routing, timings
internal/evalharness  golden loader, scoring, report, gate
policies/default.yaml synthetic policy (version 2026.2)
eval/golden.json      26 synthetic receipts with expected outcomes
examples/receipts     sample inputs, including two that dead-letter
```

## Design notes

- **Official SDKs, no framework.** The pipeline is a straight line with one model call in it, so a graph or chain abstraction would add concepts without removing code. `anthropic-sdk-go` and `openai-go` are used directly behind a two-method interface.
- **Structured output differs by provider, validation does not.** Anthropic is forced to call a `record_expense` tool whose `input_schema` is the extraction schema. OpenAI-compatible endpoints get `response_format: json_schema` with `strict: true`. Local servers may ignore that, so every provider's output goes through the same strict Go parser.
- **Amounts and dates travel as printed strings** and are parsed in Go. Floats never touch money; comparisons are in integer cents.
- **Schema violations are not retried.** The same prompt on the same document tends to fail the same way, so the document goes to the DLQ with the raw output for a person to look at. If you would rather retry once at temperature above zero, that is a one-line change in the classifier.
- **Policy findings are warn or violation.** Both route to review. The policy file is validated on load (unknown keys, unknown categories and bad values are rejected).
- **Foreign currency is not converted.** Amounts outside the base currency skip the limit rules and raise `FX_REVIEW`. A real system needs rates as of the transaction date.
- **Status:** the Anthropic and OpenAI-compatible providers are tested against local fake HTTP servers (request shape, forced tool use, strict schema, image attachment, 429/5xx/4xx classification). They have not been run against the live APIs in this repository's CI, because CI has no keys by design.

## Prior art and attribution

Checked 2026-10-08 with the GitHub API:

| Project | License | Latest release | Last push |
| --- | --- | --- | --- |
| [anthropics/anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) | MIT | v1.79.1 | 2026-10-08 |
| [openai/openai-go](https://github.com/openai/openai-go) | Apache-2.0 | v3.74.0 | 2026-10-08 |
| [google/adk-go](https://github.com/google/adk-go) | Apache-2.0 | v2.5.0 | 2026-10-08 |
| [cloudwego/eino](https://github.com/cloudwego/eino) | Apache-2.0 | v0.9.21 | 2026-09-29 |
| [tmc/langchaingo](https://github.com/tmc/langchaingo) | MIT | v0.1.15 | 2026-10-05 |
| [firebase/genkit](https://github.com/firebase/genkit) | Apache-2.0 | cli 1.44.0 | 2026-10-08 |

All are actively maintained. The two official provider SDKs are the only LLM dependencies here. ADK Go, Eino, langchaingo and Genkit are agent frameworks worth a look when you need multi-step tool loops, sessions or graph orchestration, none of which this pipeline needs. No code was copied from any of them. The forced-tool-use and strict JSON schema patterns follow the providers' own structured-output documentation. The YAML parser is [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) (MIT and Apache-2.0).

## License

MIT. See `LICENSE`.
