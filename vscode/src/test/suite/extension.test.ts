import { execFile } from "node:child_process";
import { promisify } from "node:util";

import assert from "node:assert/strict";
import { writeFile } from "node:fs/promises";
import * as path from "node:path";
import * as vscode from "vscode";

const execFileAsync = promisify(execFile);
const extensionID = "local-first-reviewer.local-code-reviewer";
const diagnosticSource = "Local Code Reviewer (local-rule)";

suite("Local Code Reviewer extension", () => {
  test("publishes and clears staged-review diagnostics", async () => {
    const folder = vscode.workspace.workspaceFolders?.[0];
    assert.ok(folder, "integration workspace folder is missing");
    const reviewerPath = process.env.REVIEWER_TEST_BINARY;
    assert.ok(reviewerPath, "REVIEWER_TEST_BINARY is missing");

    const extension = vscode.extensions.getExtension(extensionID);
    assert.ok(extension, `extension ${extensionID} is missing`);
    await extension.activate();

    const configuration = vscode.workspace.getConfiguration("localCodeReviewer", folder.uri);
    await configuration.update("binaryPath", reviewerPath, vscode.ConfigurationTarget.Global);
    await configuration.update("provider", "none", vscode.ConfigurationTarget.WorkspaceFolder);
    await configuration.update("model", "", vscode.ConfigurationTarget.WorkspaceFolder);

    await vscode.commands.executeCommand("localCodeReviewer.reviewStaged");

    const sourceURI = vscode.Uri.file(path.join(folder.uri.fsPath, "main.go"));
    const findings = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
    assert.equal(findings.length, 1);
    assert.equal(findings[0].severity, vscode.DiagnosticSeverity.Warning);
    assert.match(findings[0].message, /Potential hardcoded secret/);
    assert.equal(findings[0].range.start.line, 2);

    await writeFile(path.join(folder.uri.fsPath, "main.go"), "package sample\n\nconst safe = true\n", { mode: 0o600 });
    await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);
    await vscode.commands.executeCommand("localCodeReviewer.reviewStaged");

    const remaining = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
    assert.equal(remaining.length, 0);
  });
});
