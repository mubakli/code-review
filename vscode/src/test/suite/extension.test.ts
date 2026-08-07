import { execFile } from "node:child_process";
import { promisify } from "node:util";

import assert from "node:assert/strict";
import { writeFile } from "node:fs/promises";
import * as path from "node:path";
import * as vscode from "vscode";

const execFileAsync = promisify(execFile);
const extensionID = "local.code-review";
const diagnosticSource = "Code Review: local-rule";

suite("Code Review extension", () => {
  test("automatically reviews external staged snapshot changes", async () => {
    const folder = vscode.workspace.workspaceFolders?.[0];
    assert.ok(folder, "integration workspace folder is missing");
    const extension = vscode.extensions.getExtension(extensionID);
    assert.ok(extension, `extension ${extensionID} is missing`);
    await extension.activate();

    const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
    await configuration.update("binaryPath", "", vscode.ConfigurationTarget.Global);
    await configuration.update("provider", "none", vscode.ConfigurationTarget.Workspace);
    await configuration.update("model", "", vscode.ConfigurationTarget.Workspace);
    await configuration.update("autoReview", true, vscode.ConfigurationTarget.Workspace);
    await configuration.update("debounceMs", 300, vscode.ConfigurationTarget.Workspace);

    const sourceURI = vscode.Uri.file(path.join(folder.uri.fsPath, "main.go"));
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(sourceURI));
    await writeFile(
      path.join(folder.uri.fsPath, "main.go"),
      "package sample\n\nconst apiKey = \"another-actual-secret-value\"\n",
      { mode: 0o600 }
    );
    await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);

    const findings = await waitFor(() => {
      const values = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
      return values.length === 1 ? values : undefined;
    });
    assert.equal(findings.length, 1);
    assert.equal(findings[0].severity, vscode.DiagnosticSeverity.Warning);
    assert.match(findings[0].message, /Potential hardcoded secret/);
    assert.equal(findings[0].range.start.line, 2);

    await writeFile(path.join(folder.uri.fsPath, "main.go"), "package sample\n\nconst safe = true\n", { mode: 0o600 });
    await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);

    await waitFor(() => {
      const remaining = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
      return remaining.length === 0 ? true : undefined;
    });
    assert.notEqual(vscode.window.activeTextEditor?.document.uri.scheme, "code-review-index");
  });
});

async function waitFor<T>(read: () => T | undefined, timeoutMs = 15_000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const value = read();
    if (value !== undefined) {
      return value;
    }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error("timed out waiting for automatic review state");
}
