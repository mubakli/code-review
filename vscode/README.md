# Local-First Code Reviewer for VS Code

This extension is a thin adapter over the `reviewer` CLI. It reviews staged Git
changes and publishes structured findings to the Problems panel.

## Setup

1. Build the Go CLI: `go build -o reviewer ./cmd/reviewer`.
2. Put `reviewer` on `PATH`, or set `localCodeReviewer.binaryPath` to the built
   executable.
3. Open the trusted local Git repository root as a workspace folder.
4. Run **Local Code Reviewer: Review Staged Changes** from the Command Palette.

Local deterministic review is enabled by default. To enable OpenAI review, set
`localCodeReviewer.provider` to `openai`, set `localCodeReviewer.model`, and run
**Local Code Reviewer: Set OpenAI API Key**. The key is held in VS Code
`SecretStorage` and is passed only to the reviewer subprocess for an AI-enabled
review.

Use **Local Code Reviewer: Clear OpenAI API Key** to delete the stored key.

The extension asks for explicit repository/model approval before its first AI
review. It sends the key only to the reviewer process, and the reviewer removes
it from Git subprocess environments. The extension itself is disabled for
untrusted and virtual workspaces.

Diagnostics refer to line numbers in the staged snapshot. Unstaged edits can
shift working-tree lines, so restage and rerun the review after such changes.

## Development

```bash
npm install
npm test
```
