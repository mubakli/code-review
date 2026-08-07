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

The selected provider is always visible in two places:

- Status Bar: `AI: DeepSeek · deepseek-chat` or `AI: Off`.
- Code Review Activity Bar → **AI Provider**: provider, model, active agents,
  key status, and automatic-review status.

Click the provider status item or provider row to switch provider. Use the model
and API-key rows for direct changes without repeating the full setup flow.
Click the agents row or run **Code Review: Select AI Review Agents** to switch
between Correctness, Security, or both without changing provider or model.

After AI review, the dedicated **Code Review** Activity Bar view lists only
files that contain actual AI comments; it is separate from Source Control's full
Git change list. Selecting a comment opens a side-by-side `HEAD ↔ Staged` diff
and renders the explanation, suggested action, severity, category, and agent as
an expanded native comment thread on the staged side. Environment files and
local-only changes do not enter this view. All local and AI findings remain in
Problems.

AI completion also publishes persistent native comment threads on the matching
working-tree source lines. Opening a reviewed file therefore shows the warning
in the editor gutter and the readable provider/agent comment beside the code;
the user does not need to navigate through the full Git diff first.

AI findings identify the specialist that produced them. When selected,
Correctness runs for every eligible staged change; Security runs only when the
change contains security-relevant signals.

Findings use stable namespaced rule IDs and deterministic finding IDs. When an
AI agent can produce an exact structured replacement, **Preview Suggested Fix**
is available from the finding's lightbulb or Review Comments context menu. The
preview compares the exact reviewed staged blob with the proposed result. Apply
is always explicit and is refused if the staged snapshot changed, the path is
unsafe, or the working-tree file differs from staged content. A successful apply
creates an undoable unsaved editor change; it never saves or stages the file.

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
Packaging writes the versioned `code-review-*.vsix` artifact in this directory.
