"use strict";
var __createBinding = (this && this.__createBinding) || (Object.create ? (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    var desc = Object.getOwnPropertyDescriptor(m, k);
    if (!desc || ("get" in desc ? !m.__esModule : desc.writable || desc.configurable)) {
      desc = { enumerable: true, get: function() { return m[k]; } };
    }
    Object.defineProperty(o, k2, desc);
}) : (function(o, m, k, k2) {
    if (k2 === undefined) k2 = k;
    o[k2] = m[k];
}));
var __setModuleDefault = (this && this.__setModuleDefault) || (Object.create ? (function(o, v) {
    Object.defineProperty(o, "default", { enumerable: true, value: v });
}) : function(o, v) {
    o["default"] = v;
});
var __importStar = (this && this.__importStar) || (function () {
    var ownKeys = function(o) {
        ownKeys = Object.getOwnPropertyNames || function (o) {
            var ar = [];
            for (var k in o) if (Object.prototype.hasOwnProperty.call(o, k)) ar[ar.length] = k;
            return ar;
        };
        return ownKeys(o);
    };
    return function (mod) {
        if (mod && mod.__esModule) return mod;
        var result = {};
        if (mod != null) for (var k = ownKeys(mod), i = 0; i < k.length; i++) if (k[i] !== "default") __createBinding(result, mod, k[i]);
        __setModuleDefault(result, mod);
        return result;
    };
})();
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const node_child_process_1 = require("node:child_process");
const node_util_1 = require("node:util");
const strict_1 = __importDefault(require("node:assert/strict"));
const promises_1 = require("node:fs/promises");
const path = __importStar(require("node:path"));
const vscode = __importStar(require("vscode"));
const execFileAsync = (0, node_util_1.promisify)(node_child_process_1.execFile);
const extensionID = "local.code-review";
const diagnosticSource = "Code Review: local-rule";
suite("Code Review extension", () => {
    test("automatically reviews external staged snapshot changes", async () => {
        const folder = vscode.workspace.workspaceFolders?.[0];
        strict_1.default.ok(folder, "integration workspace folder is missing");
        const extension = vscode.extensions.getExtension(extensionID);
        strict_1.default.ok(extension, `extension ${extensionID} is missing`);
        await extension.activate();
        const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
        await configuration.update("binaryPath", "", vscode.ConfigurationTarget.Global);
        await configuration.update("provider", "none", vscode.ConfigurationTarget.Workspace);
        await configuration.update("model", "", vscode.ConfigurationTarget.Workspace);
        await configuration.update("autoReview", true, vscode.ConfigurationTarget.Workspace);
        await configuration.update("debounceMs", 300, vscode.ConfigurationTarget.Workspace);
        const sourceURI = vscode.Uri.file(path.join(folder.uri.fsPath, "main.go"));
        await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(sourceURI));
        await (0, promises_1.writeFile)(path.join(folder.uri.fsPath, "main.go"), "package sample\n\nconst apiKey = \"another-actual-secret-value\"\n", { mode: 0o600 });
        await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);
        await vscode.commands.executeCommand("git.refresh");
        const findings = await waitFor(() => {
            const values = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
            return values.length === 1 ? values : undefined;
        });
        strict_1.default.equal(findings.length, 1);
        strict_1.default.equal(findings[0].severity, vscode.DiagnosticSeverity.Warning);
        strict_1.default.match(findings[0].message, /Potential hardcoded secret/);
        strict_1.default.equal(findings[0].range.start.line, 2);
        await (0, promises_1.writeFile)(path.join(folder.uri.fsPath, "main.go"), "package sample\n\nconst safe = true\n", { mode: 0o600 });
        await execFileAsync("git", ["-C", folder.uri.fsPath, "add", "--", "main.go"]);
        await vscode.commands.executeCommand("git.refresh");
        await waitFor(() => {
            const remaining = vscode.languages.getDiagnostics(sourceURI).filter(value => value.source === diagnosticSource);
            return remaining.length === 0 ? true : undefined;
        });
        strict_1.default.notEqual(vscode.window.activeTextEditor?.document.uri.scheme, "code-review-index");
    });
});
async function waitFor(read, timeoutMs = 15_000) {
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
