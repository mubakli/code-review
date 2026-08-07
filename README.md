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

## Usage

```bash
go build -o reviewer ./cmd/reviewer
./reviewer review --staged
./reviewer review --staged --format json
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

Project-level exclusion configuration will be added with the context engine.

## Package Boundaries

- `internal/git` invokes Git directly with `exec.CommandContext`; it never
  constructs a shell command.
- `internal/gitdiff` parses staged unified patches into files, hunks, and
  line-level changes.
- `internal/pathfilter` applies privacy and generated-file exclusions.
- `internal/analyzer` hosts deterministic local analysis passes.
- `internal/findings` defines the shared finding contract.
- `internal/review` orchestrates filtering and analyzers without depending on
  VS Code.
- `internal/llm` defines the vendor-neutral provider boundary. It has no
  implementation yet.
- `internal/output` renders stable JSON or terminal output.

This separation keeps the future TypeScript extension limited to process
management, settings, secret storage, and VS Code diagnostics.

## Development

The project currently has no third-party dependencies.

```bash
go test ./...
go vet ./...
```

The next milestone is the Go symbol-aware context engine: enclosing function
resolution, direct symbol references, context ranking, token budgets, and
secret redaction before any provider request.
