import { lstat, realpath } from "node:fs/promises";
import * as path from "node:path";
import * as vscode from "vscode";

import { resolveFindingPath } from "./paths";
import { ReviewFinding } from "./protocol";
import { applyProposedFix } from "./suggestedFix";

export const fixContentScheme = "code-review-suggested";
export const previewSuggestedFixCommand = "code-review.previewSuggestedFix";
export const applySuggestedFixCommand = "code-review.applySuggestedFix";

export interface SuggestedFixSession {
  environment: NodeJS.ProcessEnv;
  executable: string;
  repositoryRoot: string;
  reviewId: string;
}

interface FixTarget {
  finding: ReviewFinding;
  session: SuggestedFixSession;
}

interface FixPreview extends FixTarget {
  staged: string;
  suggested: string;
  stagedURI: vscode.Uri;
  suggestedURI: vscode.Uri;
}

export class SuggestedFixController implements vscode.TextDocumentContentProvider, vscode.CodeActionProvider, vscode.Disposable {
  private readonly changedEmitter = new vscode.EventEmitter<vscode.Uri>();
  private readonly diagnosticTargets = new WeakMap<vscode.Diagnostic, string>();
  private readonly previews = new Map<string, FixPreview>();
  private readonly targets = new Map<string, FixTarget>();
  private sequence = 0;

  readonly onDidChange = this.changedEmitter.event;

  constructor(
    private readonly currentReviewID: (session: SuggestedFixSession, token: vscode.CancellationToken) => Promise<string>,
    private readonly stagedContent: (session: SuggestedFixSession, file: string, token: vscode.CancellationToken) => Promise<string>
  ) {}

  setReview(session: SuggestedFixSession, findings: readonly ReviewFinding[]): void {
    this.clearRepository(session.repositoryRoot);
    for (const finding of findings) {
      if (finding.proposedFix !== undefined) {
        this.targets.set(finding.findingId, { finding, session });
      }
    }
  }

  clearRepository(repositoryRoot: string): void {
    for (const [id, target] of this.targets) {
      if (target.session.repositoryRoot === repositoryRoot) {
        this.targets.delete(id);
      }
    }
    for (const [uri, preview] of this.previews) {
      if (preview.session.repositoryRoot === repositoryRoot) {
        this.previews.delete(uri);
      }
    }
  }

  registerDiagnostic(diagnostic: vscode.Diagnostic, finding: ReviewFinding): void {
    if (finding.proposedFix !== undefined && this.targets.has(finding.findingId)) {
      this.diagnosticTargets.set(diagnostic, finding.findingId);
    }
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    const preview = this.previews.get(uri.toString());
    if (preview === undefined) {
      throw new Error("Suggested fix preview is no longer available");
    }
    return uri.path.endsWith("/staged") ? preview.staged : preview.suggested;
  }

  provideCodeActions(
    _document: vscode.TextDocument,
    _range: vscode.Range | vscode.Selection,
    context: vscode.CodeActionContext
  ): vscode.CodeAction[] {
    const actions: vscode.CodeAction[] = [];
    for (const diagnostic of context.diagnostics) {
      const findingID = this.diagnosticTargets.get(diagnostic);
      if (findingID === undefined || !this.targets.has(findingID)) {
        continue;
      }
      const action = new vscode.CodeAction("Preview Suggested Fix", vscode.CodeActionKind.QuickFix);
      action.command = { command: previewSuggestedFixCommand, title: action.title, arguments: [findingID] };
      action.diagnostics = [diagnostic];
      actions.push(action);
    }
    return actions;
  }

  async preview(value: unknown): Promise<void> {
    const findingID = findingIDFrom(value);
    const target = findingID === undefined ? undefined : this.targets.get(findingID);
    if (target === undefined || target.finding.proposedFix === undefined) {
      void vscode.window.showWarningMessage("This suggested fix is no longer available. Run the review again.");
      return;
    }
    try {
      const preview = await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Window, title: "Preparing suggested fix preview", cancellable: true },
        async (_progress, token) => {
          await this.requireCurrentReview(target.session, token);
          const staged = await this.stagedContent(target.session, target.finding.file, token);
          await this.requireCurrentReview(target.session, token);
          const suggested = applyProposedFix(staged, target.finding.proposedFix!);
          const id = `${target.finding.findingId.slice(7, 23)}-${++this.sequence}`;
          const stagedURI = vscode.Uri.from({ scheme: fixContentScheme, path: `/${id}/staged` });
          const suggestedURI = vscode.Uri.from({ scheme: fixContentScheme, path: `/${id}/suggested` });
          const value: FixPreview = { ...target, staged, suggested, stagedURI, suggestedURI };
          this.previews.set(stagedURI.toString(), value);
          this.previews.set(suggestedURI.toString(), value);
          return value;
        }
      );
      await vscode.commands.executeCommand(
        "vscode.diff",
        preview.stagedURI,
        preview.suggestedURI,
        `${target.finding.file} (Staged ↔ Suggested Fix)`,
        { preview: true }
      );
    } catch (error) {
      if (!(error instanceof vscode.CancellationError)) {
        void vscode.window.showWarningMessage(`Could not preview suggested fix: ${displayError(error)}`);
      }
    }
  }

  async apply(value: unknown): Promise<void> {
    const uri = value instanceof vscode.Uri ? value : undefined;
    const preview = uri === undefined ? undefined : this.previews.get(uri.toString());
    if (preview === undefined) {
      void vscode.window.showWarningMessage("Open a current suggested fix preview before applying it.");
      return;
    }
    const approval = await vscode.window.showWarningMessage(
      `Apply this suggested fix to ${preview.finding.file}? The edit will remain unsaved and will not be staged.`,
      { modal: true },
      "Apply to Working Tree"
    );
    if (approval !== "Apply to Working Tree") {
      return;
    }
    try {
      await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Window, title: "Validating and applying suggested fix", cancellable: false },
        async (_progress, token) => {
          const targetURI = await safeTargetURI(preview.session.repositoryRoot, preview.finding.file);
          await this.requireCurrentReview(preview.session, token);
          const staged = await this.stagedContent(preview.session, preview.finding.file, token);
          await this.requireCurrentReview(preview.session, token);
          if (staged !== preview.staged) {
            throw new Error("the staged file changed after this preview was created");
          }
          const document = await vscode.workspace.openTextDocument(targetURI);
          if (document.isDirty || document.getText() !== staged) {
            throw new Error("the working-tree file has unstaged or unsaved changes");
          }
          const version = document.version;
          const edit = new vscode.WorkspaceEdit();
          edit.replace(targetURI, new vscode.Range(document.positionAt(0), document.positionAt(staged.length)), preview.suggested);
          if (document.version !== version || document.isDirty || document.getText() !== staged) {
            throw new Error("the working-tree file changed while the fix was being validated");
          }
          if (!await vscode.workspace.applyEdit(edit)) {
            throw new Error("VS Code rejected the workspace edit");
          }
          await vscode.window.showTextDocument(document);
        }
      );
      void vscode.window.showInformationMessage("Suggested fix applied as an unsaved edit. Review it, then save and stage it manually.");
    } catch (error) {
      void vscode.window.showWarningMessage(`Suggested fix was not applied: ${displayError(error)}`);
    }
  }

  dispose(): void {
    this.targets.clear();
    this.previews.clear();
    this.changedEmitter.dispose();
  }

  private async requireCurrentReview(session: SuggestedFixSession, token: vscode.CancellationToken): Promise<void> {
    if (await this.currentReviewID(session, token) !== session.reviewId) {
      throw new Error("the staged snapshot changed; run the review again");
    }
  }
}

function findingIDFrom(value: unknown): string | undefined {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "object" && value !== null && "finding" in value) {
    const finding = (value as { finding?: unknown }).finding;
    if (typeof finding === "object" && finding !== null && "findingId" in finding) {
      const id = (finding as { findingId?: unknown }).findingId;
      return typeof id === "string" ? id : undefined;
    }
  }
  return undefined;
}

async function safeTargetURI(repositoryRoot: string, file: string): Promise<vscode.Uri> {
  if (!vscode.workspace.isTrusted) {
    throw new Error("the workspace is not trusted");
  }
  const resolved = resolveFindingPath(repositoryRoot, file);
  if (resolved === undefined) {
    throw new Error("the finding path is outside the repository");
  }
  const uri = vscode.Uri.file(resolved);
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (folder === undefined || folder.uri.scheme !== "file") {
    throw new Error("the finding path is outside the open workspace");
  }
  const [rootPath, folderPath, targetPath, targetInfo] = await Promise.all([
    realpath(repositoryRoot),
    realpath(folder.uri.fsPath),
    realpath(resolved),
    lstat(resolved)
  ]);
  if (targetInfo.isSymbolicLink() || !isInside(rootPath, targetPath) || !isInside(folderPath, targetPath)) {
    throw new Error("the finding path resolves outside a writable repository file");
  }
  return uri;
}

function isInside(parent: string, candidate: string): boolean {
  const relative = path.relative(parent, candidate);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
}

function displayError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
