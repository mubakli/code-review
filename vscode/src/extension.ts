import * as vscode from "vscode";
import { spawn } from "child_process";
import * as path from "path";

const DIAGNOSTIC_COLLECTION =
  vscode.languages.createDiagnosticCollection("code-review");

interface ReviewResult {
  schemaVersion: number;
  summary: {
    filesChanged: number;
    filesReviewed: number;
    filesSkipped: number;
    hunksReviewed: number;
    addedLines: number;
    deletedLines: number;
    findingCount: number;
  };
  findings: Finding[];
}

interface Finding {
  file: string;
  startLine: number;
  endLine: number;
  severity: "critical" | "high" | "medium" | "low" | "info";
  category: string;
  title: string;
  message: string;
  suggestion: string;
  confidence: number;
  source: string;
}

function severityToDiagnosticSeverity(
  severity: string
): vscode.DiagnosticSeverity {
  switch (severity) {
    case "critical":
    case "high":
      return vscode.DiagnosticSeverity.Error;
    case "medium":
      return vscode.DiagnosticSeverity.Warning;
    case "low":
      return vscode.DiagnosticSeverity.Information;
    default:
      return vscode.DiagnosticSeverity.Hint;
  }
}

export function activate(context: vscode.ExtensionContext) {
  const disposable = vscode.commands.registerCommand(
    "code-review.reviewStaged",
    async () => {
      const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
      if (!workspaceFolder) {
        vscode.window.showErrorMessage("No workspace folder open.");
        return;
      }

      const config = vscode.workspace.getConfiguration("codeReview");
      const binaryPath =
        config.get<string>("binaryPath") ||
        path.join(context.extensionPath, "..", "..", "reviewer");
      const excludes: string[] = config.get<string[]>("exclude") || [];

      const args = [
        "review",
        "--staged",
        "--format",
        "json",
        "--repo",
        workspaceFolder.uri.fsPath,
      ];
      for (const ex of excludes) {
        args.push("--exclude", ex);
      }

      await vscode.window.withProgress(
        {
          location: vscode.ProgressLocation.Notification,
          title: "Code Review: analyzing staged changes...",
          cancellable: false,
        },
        async () => {
          try {
            const result = await runReviewer(
              binaryPath,
              args,
              workspaceFolder.uri.fsPath
            );
            applyDiagnostics(result, workspaceFolder.uri.fsPath);
            vscode.window.showInformationMessage(
              `Review complete: ${result.summary.findingCount} finding(s) in ${result.summary.filesReviewed} file(s).`
            );
          } catch (err: any) {
            vscode.window.showErrorMessage(`Code review failed: ${err.message}`);
          }
        }
      );
    }
  );

  context.subscriptions.push(disposable, DIAGNOSTIC_COLLECTION);
}

function runReviewer(
  binaryPath: string,
  args: string[],
  cwd: string
): Promise<ReviewResult> {
  return new Promise((resolve, reject) => {
    const child = spawn(binaryPath, args, { cwd });
    let stdout = "";
    let stderr = "";

    child.stdout.on("data", (data: Buffer) => {
      stdout += data.toString();
    });
    child.stderr.on("data", (data: Buffer) => {
      stderr += data.toString();
    });

    child.on("close", (code) => {
      if (code !== 0) {
        reject(new Error(stderr || `Exited with code ${code}`));
        return;
      }
      try {
        resolve(JSON.parse(stdout));
      } catch {
        reject(new Error(`Failed to parse JSON: ${stdout.slice(0, 200)}`));
      }
    });

    child.on("error", (err) => {
      reject(new Error(`Failed to start reviewer: ${err.message}`));
    });
  });
}

function applyDiagnostics(result: ReviewResult, workspaceRoot: string) {
  const diagnosticMap = new Map<string, vscode.Diagnostic[]>();

  for (const finding of result.findings) {
    const filePath = path.resolve(workspaceRoot, finding.file);
    const uri = vscode.Uri.file(filePath);

    const range = new vscode.Range(
      finding.startLine - 1, // 1-indexed to 0-indexed
      0,
      finding.endLine - 1,
      Number.MAX_SAFE_INTEGER
    );

    const message = new vscode.MarkdownString();
    message.appendMarkdown(`**${finding.title}**\n\n`);
    message.appendMarkdown(`${finding.message}\n\n`);
    if (finding.suggestion) {
      message.appendMarkdown(`*Suggestion:* ${finding.suggestion}`);
    }
    message.isTrusted = true;

    const diagnostic: vscode.Diagnostic = {
      range,
      severity: severityToDiagnosticSeverity(finding.severity),
      source: `Code Review: ${finding.source}`,
      message: `${finding.severity.toUpperCase()}: ${finding.title} — ${finding.message}`,
      code: {
        value: finding.title,
        target: vscode.Uri.parse(
          `https://github.com/search?q=${encodeURIComponent(finding.title)}`
        ),
      },
    };

    const key = uri.toString();
    if (!diagnosticMap.has(key)) {
      diagnosticMap.set(key, []);
    }
    diagnosticMap.get(key)!.push(diagnostic);
  }

  DIAGNOSTIC_COLLECTION.clear();
  for (const [uri, diagnostics] of diagnosticMap) {
    DIAGNOSTIC_COLLECTION.set(vscode.Uri.parse(uri), diagnostics);
  }
}

export function deactivate() {
  DIAGNOSTIC_COLLECTION.dispose();
}
