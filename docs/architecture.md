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
- `internal/ai` owns the declarative agent model (AgentSpec, roles, routing
  policies) and provider-neutral orchestration of prepare, build, execute,
  validate, and merge.
- `internal/ai/routing` defines the deterministic signal contract
  (`Signal`, `SignalKind`, `SecuritySurface`, `Confidence`, `SignalDetector`),
  the deduplicating `Aggregate` that policies consult before routing, and the
  tiny triage output `SecurityAssessment` (escalate, confidence, surfaces,
  reasons) that the deep security agent consumes as routing context.
- `internal/ai/routing/detectors` implements one detector per security domain
  (keyword, path, auth, network, database, filesystem, serialization,
  dependency, endpoint); each emits routing signals, never diagnosis.
- `internal/ai/egress` gates what may leave the machine toward a provider:
  `EgressRule{Pattern, Action}` with allow/redact/deny actions, applied
  before any provider-visible content is prepared or sent.
- `internal/ai/context` extracts, redacts, token-budgets, and batches a change
  set into prepared context. Its on-demand `ContextResolver` receives a
  `ContextRequest{Symbols, Paths, Intent}` naming only the surrounding areas
  (route, middleware, controller, service, repository, authorization,
  ownership) an escalated review needs, and `DiffSymbols` turns added diff
  lines into the symbols the resolver searches for. It performs no provider
  calls.
- `internal/ai/request` turns one prepared batch into a provider-neutral
  AnalysisRequest (final redaction pass, prompt attachment). It never talks to
  a network.
- `internal/ai/provider` contains the provider-neutral request/response types,
  the mock test adapter, the OpenAI Responses API adapter, and the DeepSeek
  Chat Completions adapter. Providers cannot observe unredacted content.
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
  -> ai/context
  -> ai/request
  -> ai/provider
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
  -> ai/context
  -> ai/request
  -> ai/provider
  -> ai/routing

ai/routing
  -> change

ai/routing/detectors
  -> ai/routing
  -> change

ai/egress
  -> change

ai/request
  -> ai/context
  -> findings
  -> redact

ai/provider
  -> ai/request
  -> ai/routing
  -> findings
  -> redact

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
review service. `provider.Provider` allows provider implementations without
changing safe request preparation or batching.

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

Provider implementations depend on the provider-neutral request types and the
`provider.Provider` contract. `internal/ai` will never import concrete OpenAI,
Anthropic, Gemini, or Ollama clients.

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
    provider/
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

Provider implementations import `internal/ai/request` and `internal/ai/context`
types; `internal/ai` must not import a provider implementation. Parser packages
may implement contracts consumed by the context engine; the context engine must
not hardcode every language.

Provider selection is registry-driven at the VS Code boundary and concrete at
the Go composition root. OpenAI uses its Responses API adapter; DeepSeek uses a
separate Chat Completions adapter. Both implement the provider-neutral
`provider.Provider` interface, receive only redacted `AnalysisRequest` values,
enforce bounded HTTPS responses, and keep provider-specific API keys out of
persisted settings and Git subprocesses.

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

The composition root applies a second, stricter egress policy before calling
`ai.Orchestrator`. An `egress.Policy` built from allow/redact/deny rules
(`.env`, `*.pem`, `*.key`, `secrets/**` deny; `*.config.json` redact;
`src/**`, `tests/**` allow) removes denied files from the provider-visible
change set, and the on-demand context resolver enforces the same policy for
every related file it fetches. This asymmetry is intentional: sensitive files
receive local analysis without leaving the machine.

## Privacy Boundary

The provider pipeline is: Context Resolver -> Egress Policy -> Secret Redactor
-> Token Budget -> Provider. The egress policy is the first gate: denied
content never reaches preparation or a provider, redacted content is
explicitly marked for secret redaction, and everything else still passes the
redactor before any provider can observe it.

Provider-visible requests have private fields and are created by the request
builder from a prepared batch. Redaction happens before final token
measurement, then again when the safe request is created as defense in depth.

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

`ai.Orchestrator` prepares token-budgeted batches through `ai/context`,
builds provider-neutral requests through `ai/request`, and executes them through
an injected provider implementing `provider.Provider`. The orchestrator assigns
`SourceAI` only after validating:

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
The correctness agent always reviews every eligible staged change. The security
pipeline is a three-tier gate. First, deterministic signal detectors (one per
domain: keyword, path, auth, network, database, filesystem, serialization,
dependency, endpoint) convert a change set into `Signal` values that name a
security surface, a confidence, and a reason — routing, never diagnosis.
Detectors ignore deleted lines, live in `internal/ai/routing`, and are
stateless. Second, only high-confidence signals (command execution, raw
dynamic SQL construction, eval, unsafe deserialization, credential assignment)
escalate directly without a triage call: writing `exec.Command`, a raw SQL
concatenation, or a `password` assignment is strong enough that the cheap
router would only re-confirm escalation. Third, everything else — broad terms
such as `request`, `query`, `path`, `url.`, `http.`, `auth`, and endpoint
registrations — is seeded into the lightweight triage router as routing
features, and the router decides. This keeps the cheap-triage -> expensive-
security economy intact: routine backend work (new endpoints, query handling)
stays on the triage path instead of forcing the deep agent on every change.

Triage routes, it never diagnoses. Its output is the tiny structured
`routing.SecurityAssessment` (escalate, confidence, at most a few surfaces, at
most a few short reasons) bounded to a few hundred tokens. `surfaces` are
observables from the diff that the deep agent must examine (data flows,
control-flow choices, changed boundaries) framed as areas to inspect — never
confirmed vulnerabilities, because enforcement such as authorization middleware
can live outside the diff. The same `SecurityAssessment` shape is produced on
the deterministic high-confidence path, so both escalation paths hand the deep
agent identical routing context.

The deep security agent consumes that assessment as input: the orchestrator
appends a `Security escalation context` block (potential surfaces, why the
review was escalated, "Investigate these areas first", and guidance not to
assume a vulnerability solely because an authorization check is absent from
the provided diff) to the agent's prompt.

Related context is resolved on demand and is never the changed files
themselves: the diff already shows them, so re-sending their full content
would add no information. The policy builds a `ContextRequest{Symbols, Paths,
Intent}` where `Symbols` come from `DiffSymbols`: identifiers, call targets,
and import package names extracted from the added lines, filtered against a
generic-word list and capped. The concrete resolver in the composition root
searches the Git index for those symbols (`git grep` on the index), excludes
the changed files and anything the egress policy denies, prefers the
surrounding layer the dominant surface implies (route, middleware, controller,
service, repository, authorization policy, ownership), and returns at most
three related files, redacted and budgeted downstream. The model never
receives filesystem access: it can only ask for symbols, and Go answers with
safe, bounded content. Escalation is fail-closed: any triage provider or
validation error triggers deep review.

Provider output cannot choose its identity: orchestration assigns `agentId` after
validating file membership, changed-line location, and the category allowlist.
Findings from all agents then pass through the existing deterministic-primary
deduplication.

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
