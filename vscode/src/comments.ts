import * as vscode from "vscode";

import { ReviewFinding } from "./protocol";

export class AICommentPresenter implements vscode.Disposable {
  private readonly controller = vscode.comments.createCommentController(
    "code-review.ai-comments",
    "Code Review AI Comments"
  );
  private threads: vscode.CommentThread[] = [];

  show(uri: vscode.Uri, findings: ReviewFinding[]): void {
    this.clear();
    for (const finding of findings) {
      const line = Math.max(0, finding.startLine - 1);
      const body = new vscode.MarkdownString();
      body.appendMarkdown(`### ${finding.severity.toUpperCase()} · ${finding.category}\n\n`);
      body.appendMarkdown(`**${escapeMarkdown(finding.title)}**\n\n`);
      body.appendText(finding.message);
      if (finding.suggestion !== undefined && finding.suggestion.trim() !== "") {
        body.appendMarkdown("\n\n---\n\n**Suggested action**\n\n");
        body.appendText(finding.suggestion);
      }
      body.appendMarkdown(`\n\n_Agent: ${escapeMarkdown(finding.agentId ?? "AI review")}_`);
      const comment: vscode.Comment = {
        body,
        mode: vscode.CommentMode.Preview,
        author: { name: `Code Review · ${finding.agentId ?? "AI"}` },
        label: `${finding.severity} · ${finding.category}`
      };
      const thread = this.controller.createCommentThread(
        uri,
        new vscode.Range(line, 0, line, Number.MAX_SAFE_INTEGER),
        [comment]
      );
      thread.canReply = false;
      thread.collapsibleState = vscode.CommentThreadCollapsibleState.Expanded;
      this.threads.push(thread);
    }
  }

  clear(): void {
    for (const thread of this.threads) {
      thread.dispose();
    }
    this.threads = [];
  }

  dispose(): void {
    this.clear();
    this.controller.dispose();
  }
}

function escapeMarkdown(value: string): string {
  return value.replace(/[\\`*_{}[\]()#+.!|>-]/g, "\\$&");
}
