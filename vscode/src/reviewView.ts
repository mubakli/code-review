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
    this.files = files.map(file => new ReviewFileNode(file, findingsByFile.get(file.path) ?? []));
    this.changedEmitter.fire(undefined);
  }

  firstTarget(): OpenDiffTarget | undefined {
    const first = this.files.find(file => file.findings.length > 0) ?? this.files[0];
    return first === undefined
      ? undefined
      : { file: first.file, line: first.findings[0]?.startLine };
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
      arguments: [{ file, line: findings[0]?.startLine } satisfies OpenDiffTarget]
    };
    this.tooltip = `${file.path}\n${statusLabel(file.status)} staged change`;
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
      arguments: [{ file, line: finding.startLine } satisfies OpenDiffTarget]
    };
    this.tooltip = `${finding.message}${finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`}`;
  }
}

function statusLabel(status: string): string {
  switch (status[0]) {
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
