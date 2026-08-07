# Local-First Code Reviewer

This project reviews staged Git changes before they are committed. The core is
written in Go and is designed to work independently from the future VS Code
extension.

The current milestone is deliberately small: it establishes reliable staged
diff ingestion, local path filtering, a common findings model, deterministic
local analysis, and human or JSON CLI output. It does not contact an AI
provider or read the whole repository.

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

Code intended for a future AI provider passes through a separate,
language-agnostic redaction boundary. Provider requests cannot directly set raw
diff content; `ai.Builder` redacts it locally before exposing a request to a
provider implementation.

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

This diff-only fallback works for every text-based codebase. No provider is
invoked by the CLI yet.

## Usage

```bash
go build -o reviewer ./cmd/reviewer
./reviewer review --staged
./reviewer review --staged --format json
./reviewer review --staged --repo /path/to/repository
./reviewer review --staged --exclude 'testdata/fixtures/'
```

The command returns a non-zero exit code for usage, Git, parsing, or analysis
errors. Findings do not block a commit in this milestone.

## Default Exclusions

The local analyzer currently excludes binary patches and these paths:

```text
.env
.env.*
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
- Findings are validated before output and secret values are never copied into
  finding messages.
- AI request preparation redacts credential assignments, known provider tokens,
  authorization tokens, credential-bearing URLs, and private-key bodies while
  preserving diff line structure.
- Prompt batches enforce a configurable total, diff, and static-finding token
  budget using a conservative provider-independent estimate.
- Large diffs are grouped file-first and then split at hunk/line boundaries;
  oversized lines carry an explicit truncation marker.
- No AI provider is called in this milestone.

## Package Boundaries

- `internal/change` defines the provider- and VCS-neutral changed-code model.
- `internal/git` invokes Git directly with `exec.CommandContext`; it never
  constructs a shell command, and parses staged patches into `change` values.
- `internal/pathfilter` applies privacy and generated-file exclusions.
- `internal/review` owns the local-review use case and the small analyzer port.
- `internal/analyzers/secrets` is a concrete deterministic analyzer.
- `internal/findings` defines the shared finding contract.
- `internal/redact` removes secret material before any future provider request.
- `internal/ai` owns safe requests, token budgeting, batching, and the
  vendor-neutral provider boundary and resilient orchestration.
- `internal/ai/providers/mock` provides deterministic orchestration tests.
- `internal/output` renders stable JSON or terminal output.
- `cmd/reviewer` is the composition root that selects concrete adapters.

This separation keeps the future TypeScript extension limited to process
management, settings, secret storage, and VS Code diagnostics.

See [`docs/architecture.md`](docs/architecture.md) for dependency rules,
extension points, and the planned growth structure.

## Development

The project currently has no third-party dependencies.

```bash
go test ./...
go vet ./...
```

The next milestone is the first real provider adapter plus provider/model
configuration. It will remain opt-in, use a user-owned API key, and preserve
local findings when the provider is unavailable.
