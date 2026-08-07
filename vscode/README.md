# Code Review for VS Code

This extension reviews staged Git changes through the local `reviewer` CLI.
Local deterministic rules publish Problems diagnostics without opening a diff.
The side-by-side diff workflow is reserved for files actually sent to an AI
provider.

## Setup

1. Build the CLI from the repository root: `go build -o reviewer ./cmd/reviewer`.
2. Keep the binary at the opened repository root. The extension detects it
   automatically. `codeReview.binaryPath` is only needed for another location.
3. Open a local Git workspace and stage changes. The extension reacts to the
   staged snapshot automatically; no command is required.

`git add`, `git restore --staged`, `git reset`, and Source Control stage/unstage
operations are observed through VS Code's Git repository state. Events are
debounced and the CLI's deterministic `reviewId` prevents duplicate review of
the same staged snapshot. **Code Review: Review Staged Changes** remains an
explicit force re-run command.

For local-only review, findings appear only in Problems. To enable AI review,
run **Code Review: Configure AI Provider**. The guided flow selects OpenAI or
DeepSeek, offers recommended models or a custom model ID, stores the provider's
API key in VS Code `SecretStorage`, and enables automatic AI review. Provider
keys remain separate. Choosing **Local review only** disables external requests.

After AI review, VS Code opens a side-by-side `HEAD ↔ Staged` diff. The Source
Control **AI Review** view lists only files the CLI reports as sent to the
provider and nests AI findings beneath each file. Environment files and other
local-only changes do not enter this diff view. All local and AI findings remain
available in Problems.

AI findings identify the specialist that produced them. Correctness review runs
for every eligible staged change; security review is selectively added when the
change contains security-relevant signals.

Use `codeReview.exclude` for additional repository-relative exclusion patterns.
Automatic review is controlled by `codeReview.autoReview` and
`codeReview.debounceMs`. AI automation is independently controlled by
`codeReview.ai.autoReview`. Use **Pause Automatic Reviews** and **Resume
Automatic Reviews** for large staging sessions.

## Development

```bash
npm install
npm test
npm run test:integration
npm run package:vsix
```

The Extension Host test builds the real Go CLI, creates a temporary Git
repository, and verifies staged diff navigation plus Problems diagnostics.
Packaging writes `code-review-0.1.0.vsix` in this directory.
