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
Object.defineProperty(exports, "__esModule", { value: true });
exports.activate = activate;
exports.deactivate = deactivate;
const vscode = __importStar(require("vscode"));
const child_process_1 = require("child_process");
const path = __importStar(require("path"));
const DIAGNOSTIC_COLLECTION = vscode.languages.createDiagnosticCollection("code-review");
function severityToDiagnosticSeverity(severity) {
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
function activate(context) {
    const disposable = vscode.commands.registerCommand("code-review.reviewStaged", async () => {
        const workspaceFolder = vscode.workspace.workspaceFolders?.[0];
        if (!workspaceFolder) {
            vscode.window.showErrorMessage("No workspace folder open.");
            return;
        }
        const config = vscode.workspace.getConfiguration("codeReview");
        const binaryPath = config.get("binaryPath") ||
            path.join(context.extensionPath, "..", "..", "reviewer");
        const excludes = config.get("exclude") || [];
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
        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Notification,
            title: "Code Review: analyzing staged changes...",
            cancellable: false,
        }, async () => {
            try {
                const result = await runReviewer(binaryPath, args, workspaceFolder.uri.fsPath);
                applyDiagnostics(result, workspaceFolder.uri.fsPath);
                vscode.window.showInformationMessage(`Review complete: ${result.summary.findingCount} finding(s) in ${result.summary.filesReviewed} file(s).`);
            }
            catch (err) {
                vscode.window.showErrorMessage(`Code review failed: ${err.message}`);
            }
        });
    });
    context.subscriptions.push(disposable, DIAGNOSTIC_COLLECTION);
}
function runReviewer(binaryPath, args, cwd) {
    return new Promise((resolve, reject) => {
        const child = (0, child_process_1.spawn)(binaryPath, args, { cwd });
        let stdout = "";
        let stderr = "";
        child.stdout.on("data", (data) => {
            stdout += data.toString();
        });
        child.stderr.on("data", (data) => {
            stderr += data.toString();
        });
        child.on("close", (code) => {
            if (code !== 0) {
                reject(new Error(stderr || `Exited with code ${code}`));
                return;
            }
            try {
                resolve(JSON.parse(stdout));
            }
            catch {
                reject(new Error(`Failed to parse JSON: ${stdout.slice(0, 200)}`));
            }
        });
        child.on("error", (err) => {
            reject(new Error(`Failed to start reviewer: ${err.message}`));
        });
    });
}
function applyDiagnostics(result, workspaceRoot) {
    const diagnosticMap = new Map();
    for (const finding of result.findings) {
        const filePath = path.resolve(workspaceRoot, finding.file);
        const uri = vscode.Uri.file(filePath);
        const range = new vscode.Range(finding.startLine - 1, // 1-indexed to 0-indexed
        0, finding.endLine - 1, Number.MAX_SAFE_INTEGER);
        const message = new vscode.MarkdownString();
        message.appendMarkdown(`**${finding.title}**\n\n`);
        message.appendMarkdown(`${finding.message}\n\n`);
        if (finding.suggestion) {
            message.appendMarkdown(`*Suggestion:* ${finding.suggestion}`);
        }
        message.isTrusted = true;
        const diagnostic = {
            range,
            severity: severityToDiagnosticSeverity(finding.severity),
            source: `Code Review: ${finding.source}`,
            message: `${finding.severity.toUpperCase()}: ${finding.title} — ${finding.message}`,
            code: {
                value: finding.title,
                target: vscode.Uri.parse(`https://github.com/search?q=${encodeURIComponent(finding.title)}`),
            },
        };
        const key = uri.toString();
        if (!diagnosticMap.has(key)) {
            diagnosticMap.set(key, []);
        }
        diagnosticMap.get(key).push(diagnostic);
    }
    DIAGNOSTIC_COLLECTION.clear();
    for (const [uri, diagnostics] of diagnosticMap) {
        DIAGNOSTIC_COLLECTION.set(vscode.Uri.parse(uri), diagnostics);
    }
}
function deactivate() {
    DIAGNOSTIC_COLLECTION.dispose();
}
