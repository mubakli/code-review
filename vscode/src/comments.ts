import * as vscode from "vscode";

import { resolveFindingPath } from "./paths";
import { ReviewFinding } from "./protocol";

export class AICommentPresenter implements vscode.Disposable {
  private readonly controller = vscode.comments.createCommentController(
    "code-review.ai-comments",
    "Code Review AI Comments"
  );
  private readonly sourceThreads = new Map<string, vscode.CommentThread[]>();
  private previewThreads: vscode.CommentThread[] = [];

  publishSource(repositoryRoot: string, findings: ReviewFinding[], provider: string): void {
    this.clearSource(repositoryRoot);
    const threads: vscode.CommentThread[] = [];
    for (const finding of findings) {
      const filePath = resolveFindingPath(repositoryRoot, finding.file);
      if (filePath === undefined) {
        continue;
      }
      threads.push(this.createThread(vscode.Uri.file(filePath), finding, provider));
    }
    this.sourceThreads.set(repositoryRoot, threads);
  }

  showPreview(uri: vscode.Uri, findings: ReviewFinding[], provider: string): void {
    this.clearPreview();
    this.previewThreads = findings.map(finding => this.createThread(uri, finding, provider));
  }

  clearSource(repositoryRoot: string): void {
    for (const thread of this.sourceThreads.get(repositoryRoot) ?? []) {
      thread.dispose();
    }
    this.sourceThreads.delete(repositoryRoot);
  }

  clearPreview(): void {
    for (const thread of this.previewThreads) {
      thread.dispose();
    }
    this.previewThreads = [];
  }

  clear(): void {
    for (const repositoryRoot of this.sourceThreads.keys()) {
      this.clearSource(repositoryRoot);
    }
    this.clearPreview();
  }

  dispose(): void {
    this.clear();
    this.controller.dispose();
  }

  private createThread(uri: vscode.Uri, finding: ReviewFinding, provider: string): vscode.CommentThread {
    const line = Math.max(0, finding.startLine - 1);
    const body = new vscode.MarkdownString();
    body.appendMarkdown(`### ${finding.severity.toUpperCase()} · ${finding.category}\n\n`);
    body.appendMarkdown(`**${escapeMarkdown(finding.title)}**\n\n`);
    body.appendText(finding.message);
    if (finding.suggestion !== undefined && finding.suggestion.trim() !== "") {
      body.appendMarkdown("\n\n---\n\n**Suggested action**\n\n");
      body.appendText(finding.suggestion);
    }
    body.appendMarkdown(
      `\n\n_${escapeMarkdown(provider)} · ${escapeMarkdown(finding.agentId ?? "AI review")}_`
    );
    const comment: vscode.Comment = {
      body,
      mode: vscode.CommentMode.Preview,
      author: { name: `${provider} · ${finding.agentId ?? "AI"}` },
      label: `${finding.severity} · ${finding.category}`
    };
    const thread = this.controller.createCommentThread(
      uri,
      new vscode.Range(line, 0, line, Number.MAX_SAFE_INTEGER),
      [comment]
    );
    thread.canReply = false;
    thread.collapsibleState = vscode.CommentThreadCollapsibleState.Expanded;
    return thread;
  }
}

function escapeMarkdown(value: string): string {
  return value.replace(/[\\`*_{}[\]()#+.!|>-]/g, "\\$&");
}
