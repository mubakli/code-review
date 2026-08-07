import * as path from "node:path";
import * as vscode from "vscode";

import { ReviewFinding } from "./protocol";
import { StagedFile } from "./stagedFiles";

export const reviewViewID = "code-review.reviewView";
export const openDiffCommand = "code-review.openStagedDiff";
export const baseContentScheme = "code-review-base";
export const indexContentScheme = "code-review-index";

export interface OpenDiffTarget {
  file: StagedFile;
  line?: number;
  findings: ReviewFinding[];
}

type ReviewNode = ReviewFileNode | ReviewFindingNode;

export class ReviewTreeProvider implements vscode.TreeDataProvider<ReviewNode> {
  private readonly changedEmitter = new vscode.EventEmitter<ReviewNode | undefined>();
  private files: ReviewFileNode[] = [];

  readonly onDidChangeTreeData = this.changedEmitter.event;

  setReview(files: StagedFile[], findings: ReviewFinding[]): void {
    const findingsByFile = new Map<string, ReviewFinding[]>();
    for (const finding of findings) {
      const values = findingsByFile.get(finding.file) ?? [];
      values.push(finding);
      findingsByFile.set(finding.file, values);
    }
    this.files = files
      .map(file => new ReviewFileNode(file, findingsByFile.get(file.path) ?? []))
      .filter(file => file.findings.length > 0);
    this.changedEmitter.fire(undefined);
  }

  firstTarget(): OpenDiffTarget | undefined {
    const first = this.files.find(file => file.findings.length > 0) ?? this.files[0];
    return first === undefined
      ? undefined
      : { file: first.file, line: first.findings[0]?.startLine, findings: first.findings };
  }

  getTreeItem(element: ReviewNode): vscode.TreeItem {
    return element;
  }

  getChildren(element?: ReviewNode): ReviewNode[] {
    if (element === undefined) {
      return this.files;
    }
    return element instanceof ReviewFileNode ? element.children : [];
  }
}

export class StagedContentProvider implements vscode.TextDocumentContentProvider {
  private readonly content = new Map<string, string>();
  private sequence = 0;

  clear(): void {
    this.content.clear();
  }

  add(filePath: string, base: string, staged: string): { base: vscode.Uri; staged: vscode.Uri } {
    this.sequence++;
    const documentPath = `/${this.sequence}/${filePath.replaceAll("\\", "/")}`;
    const baseURI = vscode.Uri.from({ scheme: baseContentScheme, path: documentPath });
    const stagedURI = vscode.Uri.from({ scheme: indexContentScheme, path: documentPath });
    this.content.set(baseURI.toString(), base);
    this.content.set(stagedURI.toString(), staged);
    return { base: baseURI, staged: stagedURI };
  }

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.content.get(uri.toString()) ?? "";
  }
}

class ReviewFileNode extends vscode.TreeItem {
  readonly children: ReviewFindingNode[];

  constructor(readonly file: StagedFile, readonly findings: ReviewFinding[]) {
    super(
      path.basename(file.path),
      findings.length > 0 ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.None
    );
    const directory = path.dirname(file.path);
    const findingText = findings.length === 0 ? "" : `, ${findings.length} finding${findings.length === 1 ? "" : "s"}`;
    this.description = `${statusLabel(file.status)}${findingText}${directory === "." ? "" : ` - ${directory}`}`;
    this.contextValue = "reviewFile";
    this.iconPath = new vscode.ThemeIcon("diff");
    this.command = {
      command: openDiffCommand,
      title: "Open AI Review Diff",
      arguments: [{ file, line: findings[0]?.startLine, findings } satisfies OpenDiffTarget]
    };
    const tooltip = new vscode.MarkdownString();
    tooltip.appendMarkdown(`**${file.path}**\n\n${findings.length} AI comment${findings.length === 1 ? "" : "s"}`);
    this.tooltip = tooltip;
    this.children = findings.map(finding => new ReviewFindingNode(file, finding));
  }
}

class ReviewFindingNode extends vscode.TreeItem {
  constructor(file: StagedFile, finding: ReviewFinding) {
    super(finding.title, vscode.TreeItemCollapsibleState.None);
    this.description = `line ${finding.startLine} - ${finding.severity}${finding.agentId === undefined ? "" : ` - ${finding.agentId}`}`;
    this.contextValue = "reviewFinding";
    this.iconPath = new vscode.ThemeIcon(severityIcon(finding.severity));
    this.command = {
      command: openDiffCommand,
      title: "Open AI Comment in Diff",
      arguments: [{ file, line: finding.startLine, findings: [finding] } satisfies OpenDiffTarget]
    };
    const tooltip = new vscode.MarkdownString();
    tooltip.appendMarkdown(`**${finding.severity.toUpperCase()} · ${finding.category}**\n\n`);
    tooltip.appendText(finding.message);
    if (finding.suggestion !== undefined) {
      tooltip.appendMarkdown("\n\n**Suggestion**\n\n");
      tooltip.appendText(finding.suggestion);
    }
    this.tooltip = tooltip;
  }
}

function statusLabel(status: string): string {
  switch (status[0]?.toUpperCase()) {
    case "A": return "Added";
    case "C": return "Copied";
    case "D": return "Deleted";
    case "M": return "Modified";
    case "R": return "Renamed";
    case "T": return "Type changed";
    default: return "Changed";
  }
}

function severityIcon(severity: ReviewFinding["severity"]): string {
  switch (severity) {
    case "critical":
    case "high":
      return "error";
    case "medium":
      return "warning";
    case "low":
      return "info";
    case "info":
      return "lightbulb";
  }
}
