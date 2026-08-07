# Code Review for VS Code

This extension reviews staged Git changes through the local `reviewer` CLI and
presents the result as a staged diff workflow instead of a notification-only
lint command.

## Setup

1. Build the CLI from the repository root: `go build -o reviewer ./cmd/reviewer`.
2. Set `codeReview.binaryPath` to that executable, or keep the binary at the
   repository root while developing the extension.
3. Open a local Git workspace and stage changes.
4. Run **Code Review: Review Staged Changes** from the Command Palette, the
   status bar, or the **Staged Review** view in Source Control.

After review, VS Code opens a side-by-side `HEAD ↔ Staged` diff. The Source
Control view lists every staged file and nests findings beneath its file.
Selecting a finding reopens the staged diff at the reported line. Findings are
also published to the Problems panel.

Use `codeReview.exclude` for additional repository-relative exclusion patterns.

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
