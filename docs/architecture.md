# Architecture

## Goals

The project is expected to grow from a local CLI into a local-first review
platform used by a VS Code extension, pre-commit hooks, CI jobs, and other IDEs.
It will eventually include multiple AI providers, deterministic analyzers,
language-aware context resolution, a local symbol index, configuration, and
cache management.

The architecture optimizes for these constraints:

- Repository content remains local except for explicitly prepared AI requests.
- Diff-only review works for every text-based codebase.
- Language-specific parsers enrich review but never become a requirement.
- Local analysis remains useful when an AI provider is unavailable.
- The CLI and VS Code extension use the same Go application core.
- Interfaces exist only at active substitution boundaries.
- Package names describe product capabilities rather than architecture jargon.

## Current Structure

The current capability map is intentionally higher-level than a file listing so
that routine adapter splits do not make this document stale:

- `cmd/reviewer` owns CLI parsing and the concrete composition root.
- `internal/change`, `internal/git`, and `internal/pathfilter` ingest and scope
  staged changes without exposing Git details to review logic.
- `internal/review`, `internal/analyzers/secrets`, and `internal/findings` own
  deterministic review, finding validation, and merge rules.
- `internal/ai` owns specialist agents, safe request construction, token
  budgeting, batching, and provider-neutral orchestration.
- `internal/ai/providers` contains the mock test adapter, the OpenAI Responses
  API adapter, and the DeepSeek Chat Completions adapter.
- `internal/config`, `internal/redact`, and `internal/output` own safe AI
  settings, provider-egress redaction, and CLI presentation.
- `vscode/src` is the TypeScript process/UI adapter for provider configuration,
  `SecretStorage`, staged-review automation, protocol validation, diagnostics,
  comments, and diff navigation.

Future directories are intentionally not created until a concrete feature
needs them.

## Dependency Direction

```text
cmd/reviewer
  -> git
  -> review
  -> ai
  -> ai/providers/openai
  -> ai/providers/deepseek
  -> analyzers/secrets
  -> config
  -> pathfilter
  -> output

git
  -> change

review
  -> change
  -> findings
  -> pathfilter

analyzers/secrets
  -> change
  -> findings

ai
  -> change
  -> findings
  -> redact

ai/providers/openai, ai/providers/deepseek
  -> ai

output
  -> review
  -> findings
```

Core use cases must not import concrete input adapters, analyzers, provider
implementations, configuration loaders, or presentation packages. The
composition root is the only place that selects concrete implementations.

`internal/review/architecture_test.go` enforces the most important forbidden
dependencies.

## SOLID Decisions

### Single Responsibility

Git command execution and Git patch parsing live together because the parser
expects the exact deterministic format produced by that adapter. The shared
change model is separate because review, analyzers, and AI preparation all use
it without needing Git.

Local analyzer implementations do not accumulate in one flat package. Large
capabilities receive focused packages under `internal/analyzers`.

AI request safety, provider contracts, and batching belong to one cohesive AI
feature package. Secret redaction remains a leaf security package because its
egress policy intentionally differs from conservative secret findings.

### Open/Closed

`review.Analyzer` allows new deterministic analyzers without changing the
review service. `ai.Provider` allows provider implementations without changing
safe request construction or batching.

Future parser and cache interfaces will be defined by their consuming use case
only after the first concrete implementation exists.

### Liskov Substitution

Analyzer and provider implementations must honor context cancellation, return
validated structured values, and avoid hidden network or repository access not
implied by their contract.

### Interface Segregation

Current interfaces contain one operation plus only metadata actively required
for errors. There are no broad repository, configuration, tokenizer, renderer,
or service-locator interfaces.

### Dependency Inversion

`review` owns the analyzer interface it consumes. It receives parsed changes
and does not know how Git, editor buffers, commit ranges, or future sources
produce them.

Provider implementations will depend on the `ai.Provider` contract. The AI
package will never import concrete OpenAI, Anthropic, Gemini, or Ollama clients.

## Composition Root

`cmd/reviewer/wire.go` performs staged-review composition:

```text
validated CLI options
  -> Git repository
  -> parsed staged changes
  -> path filter
  -> configured local analyzers
  -> review service
  -> output renderer
```

This concrete wiring in `main` is intentional. A dependency-injection
framework or service locator would hide dependencies without adding value.

If wiring grows, it should be split into additional files in the `main`
package, not moved into the review core.

## Growth Map

Add these packages only when their features are implemented:

```text
internal/
  ai/
    providers/
      anthropic/
      gemini/
      ollama/
  analyzers/
    security/
    sql/
    performance/
    quality/
  contextengine/
  parsers/
    golang/
    typescript/
    python/
  index/
  cache/
  protocol/
```

`contextengine` is used instead of `context` to avoid collisions with Go's
standard `context` package.

Provider packages may import `internal/ai`; `internal/ai` must not import a
provider implementation. Parser packages may implement contracts consumed by
the context engine; the context engine must not hardcode every language.

Provider selection is registry-driven at the VS Code boundary and concrete at
the Go composition root. OpenAI uses its Responses API adapter; DeepSeek uses a
separate Chat Completions adapter. Both implement `ai.Provider`, receive only
redacted `AnalysisRequest` values, enforce bounded HTTPS responses, and keep
provider-specific API keys out of persisted settings and Git subprocesses.

The VS Code TypeScript layer remains a process and UI adapter. It starts the Go
binary, stores API keys in `SecretStorage`, maps JSON findings to diagnostics,
and manages settings. Review policy does not move into TypeScript.

VS Code's built-in Git repository events are treated only as staged-state
hints. After a trailing debounce, the adapter asks the Go CLI for the
authoritative staged `reviewId`, derived from the same deterministic patch used
for review. Running/completed IDs are deduplicated, changed IDs cancel stale
work, and `--expected-review-id` prevents publication from a different index
snapshot. Local review publishes before optional AI review starts.

## Review Scope Policy

Path and binary filtering first creates the immutable local review scope through
`review.ScopeChanges`. Local analyzers inspect all files in that scope,
including `.env` and `.env.*`, so deterministic secret detection still runs on
sensitive configuration.

The composition root applies a second, stricter AI egress policy before calling
`ai.Orchestrator`. Environment files are removed from that provider-visible
change set. `ai.Builder` accepts the egress-safe set and retains defensive
pathless and binary checks. This asymmetry is intentional: sensitive files
receive local analysis without leaving the machine.

## Privacy Boundary

Provider-visible requests have private fields and are created by the AI
builder. Redaction happens before final token measurement, then again when the
safe request is created as defense in depth.

Provider implementations receive only:

- Compact instructions.
- Redacted, budgeted diff fragments.
- Relevant validated local findings.

They do not receive repository handles, absolute repository paths, API keys,
environment-file diffs, or permission to resolve additional files directly.

API keys are runtime secrets, not configuration values. The CLI accepts the
OpenAI key only through `REVIEWER_OPENAI_API_KEY` and the DeepSeek key only
through `REVIEWER_DEEPSEEK_API_KEY`. The VS Code adapter reads the selected
provider's key from `SecretStorage` and adds it only to an AI-enabled reviewer
process. The Git adapter removes both variables before starting Git. Provider
and model names may be persisted because they are not secret. AI egress also
requires explicit repository/model approval in the extension.

The OpenAI adapter uses the Responses API with `store: false`, strict JSON
schema output, context-aware HTTP requests, HTTPS-only remote endpoints, and a
bounded response body. SDK-specific request and response types remain inside
the provider package.

The DeepSeek adapter uses the Chat Completions API with JSON output and the same
context cancellation, HTTPS-only endpoint, response-size, and finding
validation boundaries. DeepSeek-specific wire types remain inside its provider
package.

## AI Orchestration

`ai.Orchestrator` executes token-budgeted batches through an injected
`ai.Provider`. Provider responses use an AI-specific DTO without a source
field. The orchestrator assigns `SourceAI` only after validating:

- Response status.
- Finding shape, enums, confidence, and line ranges.
- Membership in the current batch.
- A start line introduced by the scoped change set.

Provider errors, nil responses, and invalid batches are recorded as
`BatchFailure` values. They do not remove local findings and do not stop later
batches. Context cancellation and local batch-construction errors remain fatal.

`AISummary.reviewedFiles` records the unique files in provider requests that
were actually attempted. The VS Code adapter uses this list as the authority
for its AI diff view; it does not infer provider egress from the raw staged file
list. Local-only and AI-excluded files therefore remain outside that view.

Local and AI findings are merged with deterministic findings as the primary
record. Likely duplicates require the same file, category, overlapping lines,
and a matching issue concept or strong text similarity. A duplicate AI finding
may raise severity or confidence and fill a missing suggestion, but it does not
replace the deterministic title, message, or source.

Specialist review agents are provider-neutral values owned by `internal/ai`.
`CorrectnessAgent` is always routed; `SecurityAgent` is routed only from
deterministic changed-line/path signals. Every routed agent builds its own
redacted, token-budgeted request batches. Provider output cannot choose its
identity: orchestration assigns `agentId` after validating file membership,
changed-line location, and the category allowlist for that agent. Findings from
all agents then pass through the existing deterministic-primary deduplication.

## Wire Contracts

`review.Result` currently doubles as the CLI JSON response. This is deliberate
while the application is small. Schema version 3 is the current contract for
both review and staged-snapshot envelopes. Version 3 findings carry a required
namespaced `ruleId`, a deterministic SHA-256 `findingId`, and an optional
bounded whole-line `proposedFix` whose range must match the finding. Reviewed-file
metadata, AI agent identities, and routed-agent summaries remain part of the
contract. The VS Code adapter accepts version 3 and validates every envelope,
file, summary, finding identity, and proposed fix before publishing diagnostics,
comments, or edits. Future wire changes must use a new schema version and
matching CLI and adapter tests.

A separate protocol DTO should be introduced only when editor and CLI wire
needs actually diverge from application values.

## Rules For New Code

- Put a type in the lowest-level package that can own it without importing an
  adapter.
- Define interfaces in the package that consumes them.
- Prefer concrete structs until a second implementation or testing seam exists.
- Keep provider SDK types inside their provider package.
- Keep language AST types inside their parser package.
- Never let provider or parser details leak into `change`, `findings`, or
  `review`.
- Do not create shared utility packages; place helpers with the behavior they
  support.
- Preserve local-only operation when optional AI or indexing components fail.
