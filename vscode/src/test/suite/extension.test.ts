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
  test("opens staged diffs and updates diagnostics", async () => {
    const folder = vscode.workspace.workspaceFolders?.[0];
    assert.ok(folder, "integration workspace folder is missing");
    const reviewerPath = process.env.REVIEWER_TEST_BINARY;
    assert.ok(reviewerPath, "REVIEWER_TEST_BINARY is missing");

    const extension = vscode.extensions.getExtension(extensionID);
    assert.ok(extension, `extension ${extensionID} is missing`);
    await extension.activate();

    const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
    await configuration.update("binaryPath", reviewerPath, vscode.ConfigurationTarget.Global);

    await vscode.commands.executeCommand("code-review.reviewStaged");

    const stagedEditor = vscode.window.activeTextEditor;
    assert.ok(stagedEditor, "staged diff editor did not open");
    assert.equal(stagedEditor.document.uri.scheme, "code-review-index");
    assert.match(stagedEditor.document.getText(), /actual-secret-value-123/);

    const sourceURI = vscode.Uri.file(path.join(folder.uri.fsPath, "main.go"));
    const findings = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
    assert.equal(findings.length, 1);
    assert.equal(findings[0].severity, vscode.DiagnosticSeverity.Warning);
    assert.match(findings[0].message, /Potential hardcoded secret/);
    assert.equal(findings[0].range.start.line, 2);

    await writeFile(path.join(folder.uri.fsPath, "main.go"), "package sample\n\nconst safe = true\n", { mode: 0o600 });
    await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);
    await vscode.commands.executeCommand("code-review.reviewStaged");

    const cleanStagedEditor = vscode.window.activeTextEditor;
    assert.ok(cleanStagedEditor, "updated staged diff editor did not open");
    assert.equal(cleanStagedEditor.document.uri.scheme, "code-review-index");
    assert.match(cleanStagedEditor.document.getText(), /const safe = true/);

    const remaining = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
    assert.equal(remaining.length, 0);
  });
});
