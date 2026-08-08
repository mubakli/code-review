# Local-First Code Reviewer

This project reviews staged Git changes before they are committed. The Go CLI
works independently and a thin VS Code adapter exposes the same review through
editor commands and the Problems panel.

The implementation includes reliable staged diff ingestion, deterministic
local analysis, optional AI review, human or JSON CLI output, and a minimal VS
Code process adapter. It does not require whole-repository indexing.

## Current Workflow

```text
git diff --cached
        |
        v
unified diff parser
        |
        v
default path and binary filtering
        |
        v
local analyzers
        |
        v
human or JSON findings
```

The first local rule checks added lines for likely hardcoded credentials. A
finding contains only the location and remediation guidance; the detected
value is never copied into output.

Code intended for an AI provider passes through a separate,
language-agnostic redaction boundary. Provider requests cannot directly set raw
diff content; the context layer redacts and budgets it, and the request layer
redacts it again before exposing a request to a provider implementation.

The provider preparation path is also language-independent:

```text
filtered text changes
        |
        v
local secret redaction
        |
        v
file/hunk-aware token batching
        |
        v
budgeted AnalysisRequest values
```

This diff-only fallback works for every text-based codebase. The CLI invokes a
provider only when AI review is explicitly enabled.

AI review uses selectable specialist agents rather than one unrestricted prompt.
When enabled, the correctness agent runs for every eligible staged change. The
security pipeline first evaluates deterministic signal detectors; only
high-confidence signals (command execution, raw dynamic SQL construction, eval,
unsafe deserialization, credential assignment) escalate directly without a
triage call. Otherwise a lightweight triage classifier runs on the redacted
diff, seeded with the detected medium/low signals (request, query, path, url.,
auth, endpoint registration, ...) as routing features. Triage output is the
tiny structured `SecurityAssessment` (escalate, confidence, a few surfaces, a
few reasons) and is consumed as input by the deep security specialist: the
escalated surfaces and reasons are appended to its prompt, and related staged
context is resolved on demand — symbols extracted from the added lines are
searched in the Git index, changed files are never re-sent, and only the 1-3
surrounding files the symbols point at are fetched, egress-checked, redacted,
and budgeted. The deep agent runs when a signal, the triage, or any triage
error triggers it. Escalation is fail-closed. Each path
receives the same redacted, budgeted diff pipeline; Go assigns the trusted
`agentId`, filters out-of-scope categories, and merges likely duplicates before
returning findings.

Every finding carries a stable namespaced `ruleId` and a deterministic
`findingId`. AI findings may also contain a bounded structured `proposedFix`
only when the agent can replace the complete added-line range exactly. The VS
Code adapter previews the reviewed staged blob against that proposal and applies
it only after explicit confirmation and stale/content/path validation. Apply
creates an unsaved working-tree edit and never saves or stages automatically.

## Usage

```bash
go build -o reviewer ./cmd/reviewer
./reviewer review --staged
./reviewer review --staged --format json
./reviewer snapshot --staged
./reviewer review --staged --repo /path/to/repository
./reviewer review --staged --exclude 'testdata/fixtures/'

export REVIEWER_OPENAI_API_KEY='your-key'
./reviewer review --staged --ai-provider openai --ai-model your-model
./reviewer review --staged --ai-provider openai --ai-model your-model --ai-agent correctness

export REVIEWER_DEEPSEEK_API_KEY='your-key'
./reviewer review --staged --ai-provider deepseek --ai-model deepseek-chat
./reviewer review --staged --ai-provider deepseek --ai-model deepseek-chat --ai-agent security
```

Repeat `--ai-agent` to combine specialists. If omitted, Correctness and Security
are enabled; Security is still invoked only when the staged change contains a
security-relevant signal.

The command returns a non-zero exit code for usage, Git, parsing, or analysis
errors. Findings do not block a commit in this milestone.

The VS Code adapter treats a staged snapshot change as the normal review
trigger. Git/SCM events are debounced, then the CLI derives a deterministic
`sha256:` review ID from the exact staged patch. Duplicate snapshots are
skipped and stale local or AI results are cancelled or discarded. The manual
review command remains available as a force re-run.

AI review is opt-in. OpenAI and DeepSeek are currently supported. Provider and model are safe settings, but API keys are
never accepted as command-line flags or repository configuration. The CLI
reads the OpenAI key from `REVIEWER_OPENAI_API_KEY`; the VS Code adapter sources
it from `SecretStorage` and supplies it only for AI-enabled review.

## Default Exclusions

The local analyzer currently excludes binary patches and these generated or
dependency paths:

```text
node_modules/
vendor/
dist/
build/
bin/
obj/
coverage/
generated/
.git/
```

Additional patterns can be supplied with repeatable `--exclude` flags. Invalid
patterns are rejected instead of silently failing open. Project-level
configuration will be added with the context engine.

## Safety And Privacy

- Git is executed directly with argument arrays, never through a shell.
- Ambient Git variables that could redirect the repository or index are
  removed from the subprocess environment.
- External diff and text-conversion helpers are disabled.
- Staged diff output is capped at 32 MiB to bound local memory usage.
- Excluded and binary files do not enter local analyzers.
- `.env` and `.env.*` files are analyzed locally for secrets but are never sent
  to AI providers. Placeholder values in files such as `.env.example` are
  ignored by the secret analyzer.
- Findings are validated before output and secret values are never copied into
  finding messages.
- AI request preparation redacts credential assignments, known provider tokens,
  authorization tokens, credential-bearing URLs, and private-key bodies while
  preserving diff line structure.
- Prompt batches enforce a configurable total, diff, and static-finding token
  budget using a conservative provider-independent estimate.
- Large diffs are grouped file-first and then split at hunk/line boundaries;
  oversized lines carry an explicit truncation marker.
- AI review is disabled by default and never runs without explicit provider and
  model settings.
- Provider egress follows a rule-based policy (`internal/ai/egress`): `.env`,
  `*.pem`, `*.key`, and `secrets/**` are denied, `*.config.json` is redacted,
  and every other file still passes the redactor before a provider can observe
  it.
- The deep security agent never receives filesystem access: related context is
  resolved on demand from the Git index by symbol lookup and returned redacted
  and token-budgeted.
- OpenAI requests set `store: false`, use strict structured output, and bound
  response size. Provider failures preserve local findings.

## Package Boundaries

- `internal/change` defines the provider- and VCS-neutral changed-code model.
- `internal/git` invokes Git directly with `exec.CommandContext`; it never
  constructs a shell command, and parses staged patches into `change` values.
- `internal/pathfilter` applies generated-file exclusions for local review.
- `internal/review` owns the local-review use case and the small analyzer port.
- `internal/analyzers/secrets` is a concrete deterministic analyzer.
- `internal/findings` defines the shared finding contract.
- `internal/redact` removes secret material before any future provider request.
- `internal/ai` owns the declarative agent model, routing policies, and
  provider-neutral orchestration.
- `internal/ai/routing` defines the deterministic signal contract
  (`SignalDetector`, `Signal`, `SignalKind`, `SecuritySurface`, `Confidence`),
  the deduplicating `Aggregate` used by the security escalation policy, and the
  tiny triage output `SecurityAssessment`.
- `internal/ai/routing/detectors` implements one detector per security domain
  (keyword, path, auth, network, database, filesystem, serialization,
  dependency, endpoint); each emits routing signals, never diagnosis.
- `internal/ai/egress` gates provider egress with allow/redact/deny rules
  (`EgressRule{Pattern, Action}`) applied before any content leaves the
  machine.
- `internal/ai/context` extracts, redacts, token-budgets, and batches a change
  set; it never talks to providers. Its on-demand `ContextResolver` accepts a
  `ContextRequest{Symbols, Paths, Intent}` naming only the surrounding areas an
  escalated review needs.
- `internal/ai/request` converts one prepared batch into a provider-neutral
  `AnalysisRequest` with a final redaction pass.
- `internal/ai/provider` defines the provider boundary, the mock test adapter,
  the OpenAI Responses adapter, and the bounded DeepSeek Chat Completions
  adapter behind the same provider-neutral contract.
- `internal/config` validates safe provider and model settings and never stores
  API keys.
- `internal/output` renders stable JSON or terminal output.
- `cmd/reviewer` is the composition root that selects concrete adapters.
- `vscode` is the TypeScript process/UI adapter for settings, `SecretStorage`,
  staged-review commands, and Problems panel diagnostics.

This separation keeps the future TypeScript extension limited to process
management, settings, secret storage, and VS Code diagnostics.

See [`docs/architecture.md`](docs/architecture.md) for dependency rules,
extension points, and the planned growth structure.

## Development

The Go core has no third-party dependencies. The VS Code adapter uses
development-only TypeScript and VS Code type packages.

```bash
go test ./...
go vet ./...
cd vscode
npm install
npm test
npm run test:integration
npm run package:vsix
```

See [`vscode/README.md`](vscode/README.md) for extension setup and commands.
