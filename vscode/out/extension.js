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
const node_child_process_1 = require("node:child_process");
const path = __importStar(require("node:path"));
const vscode = __importStar(require("vscode"));
const environment_1 = require("./environment");
const paths_1 = require("./paths");
const protocol_1 = require("./protocol");
const reviewView_1 = require("./reviewView");
const stagedFiles_1 = require("./stagedFiles");
const reviewCommand = "code-review.reviewStaged";
const maxOutputBytes = 32 * 1024 * 1024;
const maxErrorBytes = 1024 * 1024;
const activeChildren = new Set();
function activate(context) {
    const diagnostics = vscode.languages.createDiagnosticCollection("code-review");
    const reviewTree = new reviewView_1.ReviewTreeProvider();
    const treeView = vscode.window.createTreeView(reviewView_1.reviewViewID, { treeDataProvider: reviewTree });
    const stagedContent = new reviewView_1.StagedContentProvider();
    const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
    status.name = "Code Review";
    status.text = "$(diff) Review Staged";
    status.tooltip = "Review staged changes and open the staged diff";
    status.command = reviewCommand;
    status.show();
    let running = false;
    let session;
    context.subscriptions.push(diagnostics, treeView, status, vscode.workspace.registerTextDocumentContentProvider(reviewView_1.baseContentScheme, stagedContent), vscode.workspace.registerTextDocumentContentProvider(reviewView_1.indexContentScheme, stagedContent), vscode.commands.registerCommand(reviewView_1.openDiffCommand, async (target) => {
        if (target === undefined || session === undefined) {
            void vscode.window.showInformationMessage("Run a staged review before opening its diff.");
            return;
        }
        await openStagedDiff(session, target, stagedContent);
    }), vscode.commands.registerCommand(reviewCommand, async () => {
        if (running) {
            void vscode.window.showInformationMessage("A staged review is already running.");
            return;
        }
        running = true;
        status.text = "$(loading~spin) Reviewing Staged";
        try {
            const folder = selectWorkspaceFolder();
            const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
            const binaryPath = configuration.get("binaryPath")?.trim()
                || path.join(context.extensionPath, "..", "reviewer");
            const excludes = configuration.get("exclude", []);
            const environment = (0, environment_1.buildReviewerEnvironment)(process.env);
            const execution = await vscode.window.withProgress({
                location: vscode.ProgressLocation.Notification,
                title: `Reviewing staged changes in ${folder.name}`,
                cancellable: true
            }, async (_progress, token) => {
                const repositoryRoot = (await runProcess("git", ["-C", folder.uri.fsPath, "rev-parse", "--show-toplevel"], folder.uri.fsPath, environment, token, 64 * 1024)).trim();
                if (repositoryRoot === "" || !path.isAbsolute(repositoryRoot)) {
                    throw new Error("Git returned an invalid repository root");
                }
                const stagedFiles = await discoverStagedFiles(repositoryRoot, environment, token);
                const args = ["review", "--staged", "--format", "json", "--repo", repositoryRoot];
                for (const exclude of excludes) {
                    args.push("--exclude", exclude);
                }
                const output = await runProcess(binaryPath, args, repositoryRoot, environment, token, maxOutputBytes);
                return { repositoryRoot, result: (0, protocol_1.parseReviewResult)(output), stagedFiles };
            });
            session = { environment, repositoryRoot: execution.repositoryRoot };
            stagedContent.clear();
            applyDiagnostics(diagnostics, execution.result, folder.uri.fsPath);
            reviewTree.setReview(execution.stagedFiles, execution.result.findings);
            treeView.description = `${execution.stagedFiles.length} files, ${execution.result.findings.length} findings`;
            treeView.message = execution.stagedFiles.length === 0 ? "No staged changes." : undefined;
            const firstTarget = reviewTree.firstTarget();
            if (firstTarget !== undefined) {
                await vscode.commands.executeCommand("workbench.view.scm");
                await openStagedDiff(session, firstTarget, stagedContent);
            }
            void vscode.window.showInformationMessage(`Staged review complete: ${execution.result.findings.length} finding(s) across ${execution.stagedFiles.length} staged file(s).`);
        }
        catch (error) {
            if (!(error instanceof vscode.CancellationError)) {
                void vscode.window.showErrorMessage(`Code review failed: ${errorMessage(error)}`);
            }
        }
        finally {
            running = false;
            status.text = "$(diff) Review Staged";
        }
    }));
}
function deactivate() {
    for (const child of activeChildren) {
        child.kill();
    }
    activeChildren.clear();
}
function selectWorkspaceFolder() {
    const folders = vscode.workspace.workspaceFolders?.filter(folder => folder.uri.scheme === "file") ?? [];
    if (folders.length === 0) {
        throw new Error("Open a local workspace containing a Git repository first");
    }
    const activeURI = vscode.window.activeTextEditor?.document.uri;
    return (activeURI === undefined ? undefined : vscode.workspace.getWorkspaceFolder(activeURI)) ?? folders[0];
}
async function discoverStagedFiles(repositoryRoot, environment, token) {
    const output = await runProcess("git", [
        "-C",
        repositoryRoot,
        "-c",
        "core.quotePath=false",
        "diff",
        "--cached",
        "--name-status",
        "-z",
        "--find-renames=50%",
        "--"
    ], repositoryRoot, environment, token, maxOutputBytes);
    return (0, stagedFiles_1.parseNameStatus)(output);
}
async function openStagedDiff(session, target, contentProvider) {
    try {
        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Window,
            title: `Opening staged diff for ${target.file.path}`,
            cancellable: true
        }, async (_progress, token) => {
            const basePath = target.file.previousPath ?? target.file.path;
            const base = target.file.status.startsWith("A")
                ? ""
                : await readGitFile(session, `HEAD:${basePath}`, token);
            const staged = target.file.status.startsWith("D")
                ? ""
                : await readGitFile(session, `:${target.file.path}`, token);
            if (base.includes("\0") || staged.includes("\0")) {
                throw new Error(`Binary staged diff cannot be displayed for ${target.file.path}`);
            }
            const documents = contentProvider.add(target.file.path, base, staged);
            const line = Math.max(0, (target.line ?? 1) - 1);
            await vscode.commands.executeCommand("vscode.diff", documents.base, documents.staged, `${target.file.path} (HEAD ↔ Staged)`, {
                preview: true,
                selection: new vscode.Range(line, 0, line, 0)
            });
        });
    }
    catch (error) {
        if (!(error instanceof vscode.CancellationError)) {
            void vscode.window.showWarningMessage(`Could not open staged diff: ${errorMessage(error)}`);
        }
    }
}
function readGitFile(session, object, token) {
    return runProcess("git", ["-C", session.repositoryRoot, "show", object], session.repositoryRoot, session.environment, token, maxOutputBytes);
}
function applyDiagnostics(collection, result, repositoryRoot) {
    const grouped = new Map();
    for (const finding of result.findings) {
        const filePath = (0, paths_1.resolveFindingPath)(repositoryRoot, finding.file);
        if (filePath === undefined) {
            continue;
        }
        const uri = vscode.Uri.file(filePath);
        const key = uri.toString();
        const entry = grouped.get(key) ?? { uri, values: [] };
        entry.values.push(toDiagnostic(finding));
        grouped.set(key, entry);
    }
    collection.clear();
    for (const entry of grouped.values()) {
        collection.set(entry.uri, entry.values);
    }
}
function toDiagnostic(finding) {
    const diagnostic = new vscode.Diagnostic(new vscode.Range(finding.startLine - 1, 0, finding.endLine - 1, Number.MAX_SAFE_INTEGER), `${finding.title}\n\n${finding.message}${finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`}`, diagnosticSeverity(finding.severity));
    diagnostic.source = `Code Review: ${finding.source}`;
    diagnostic.code = finding.category;
    return diagnostic;
}
function diagnosticSeverity(severity) {
    switch (severity) {
        case "critical":
        case "high":
            return vscode.DiagnosticSeverity.Error;
        case "medium":
            return vscode.DiagnosticSeverity.Warning;
        case "low":
            return vscode.DiagnosticSeverity.Information;
        case "info":
            return vscode.DiagnosticSeverity.Hint;
    }
}
function runProcess(executable, args, cwd, environment, token, stdoutLimit) {
    return new Promise((resolve, reject) => {
        const child = (0, node_child_process_1.spawn)(executable, args, {
            cwd,
            env: environment,
            shell: false,
            windowsHide: true,
            stdio: ["ignore", "pipe", "pipe"]
        });
        activeChildren.add(child);
        const stdout = [];
        const stderr = [];
        let stdoutBytes = 0;
        let stderrBytes = 0;
        let settled = false;
        let stopError;
        let cancellation;
        const finish = (action) => {
            if (settled) {
                return;
            }
            settled = true;
            activeChildren.delete(child);
            cancellation?.dispose();
            action();
        };
        const stop = (error) => {
            if (stopError !== undefined) {
                return;
            }
            stopError = error;
            child.kill("SIGTERM");
        };
        child.stdout.on("data", (chunk) => {
            stdoutBytes += chunk.length;
            if (stdoutBytes > stdoutLimit) {
                stop(new Error(`${executable} output exceeded the safe limit`));
                return;
            }
            stdout.push(chunk);
        });
        child.stderr.on("data", (chunk) => {
            stderrBytes += chunk.length;
            if (stderrBytes > maxErrorBytes) {
                stop(new Error(`${executable} error output exceeded the safe limit`));
                return;
            }
            stderr.push(chunk);
        });
        child.on("error", error => finish(() => reject(executableError(executable, error))));
        child.on("close", code => finish(() => {
            if (stopError !== undefined) {
                reject(stopError);
                return;
            }
            if (code === 0) {
                resolve(Buffer.concat(stdout).toString("utf8"));
                return;
            }
            const detail = Buffer.concat(stderr).toString("utf8").trim();
            reject(new Error(detail === "" ? `${executable} exited with code ${code ?? "unknown"}` : detail));
        }));
        cancellation = token.onCancellationRequested(() => stop(new vscode.CancellationError()));
        if (token.isCancellationRequested) {
            stop(new vscode.CancellationError());
        }
    });
}
function executableError(executable, error) {
    if (error.code === "ENOENT") {
        return new Error(`Executable ${JSON.stringify(executable)} was not found; configure codeReview.binaryPath`);
    }
    return error;
}
function errorMessage(error) {
    return error instanceof Error ? error.message : String(error);
}
