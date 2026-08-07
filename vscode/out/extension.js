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
const node_fs_1 = require("node:fs");
const path = __importStar(require("node:path"));
const vscode = __importStar(require("vscode"));
const aiReview_1 = require("./aiReview");
const autoReview_1 = require("./autoReview");
const environment_1 = require("./environment");
const gitApi_1 = require("./gitApi");
const paths_1 = require("./paths");
const providers_1 = require("./providers");
const protocol_1 = require("./protocol");
const reviewView_1 = require("./reviewView");
const reviewCommand = "code-review.reviewStaged";
const setKeyCommand = "code-review.setOpenAIAPIKey";
const clearKeyCommand = "code-review.clearOpenAIAPIKey";
const configureProviderCommand = "code-review.configureAIProvider";
const pauseCommand = "code-review.pauseAutoReview";
const resumeCommand = "code-review.resumeAutoReview";
const maxOutputBytes = 32 * 1024 * 1024;
const maxErrorBytes = 1024 * 1024;
const activeChildren = new Set();
async function activate(context) {
    const diagnostics = vscode.languages.createDiagnosticCollection("code-review");
    const diagnosticUris = new Map();
    const reviewTree = new reviewView_1.ReviewTreeProvider();
    const treeView = vscode.window.createTreeView(reviewView_1.reviewViewID, { treeDataProvider: reviewTree });
    const stagedContent = new reviewView_1.StagedContentProvider();
    const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
    const output = vscode.window.createOutputChannel("Code Review", { log: true });
    const knownRepositories = new Set();
    const interactiveRepositories = new Set();
    let currentSession;
    let paused = context.globalState.get("autoReviewPaused", false);
    status.name = "Code Review";
    status.command = "workbench.actions.view.problems";
    setStatus(status, paused ? "Code Review: Paused" : "Code Review");
    status.show();
    const scheduler = new autoReview_1.AutoReviewScheduler(500, async (repositoryRoot, signal) => {
        const configuration = await readConfiguration(context, repositoryRoot);
        const snapshot = await readSnapshot(repositoryRoot, configuration.executable, signal);
        const provider = (0, providers_1.providerByID)(configuration.provider);
        const hasKey = provider === undefined
            ? false
            : (await context.secrets.get(provider.secretKey))?.trim() !== "";
        return {
            reviewId: snapshot.reviewId,
            dedupeKey: [
                snapshot.reviewId,
                configuration.executable,
                configuration.excludes.join("\0"),
                configuration.aiEnabled,
                configuration.aiAutoReview,
                configuration.provider,
                configuration.model,
                hasKey
            ].join("|")
        };
    }, async (repositoryRoot, snapshot, signal) => {
        const interactive = interactiveRepositories.has(repositoryRoot);
        const configuration = await readConfiguration(context, repositoryRoot);
        clearRepositoryDiagnostics(diagnostics, diagnosticUris, repositoryRoot);
        if (currentSession?.repositoryRoot === repositoryRoot) {
            currentSession = undefined;
            clearAIView(reviewTree, treeView, stagedContent);
        }
        const localResult = await runReviewPhase(repositoryRoot, snapshot.reviewId, configuration, undefined, signal);
        await requireCurrentSnapshot(repositoryRoot, configuration.executable, snapshot.reviewId, signal);
        publishDiagnostics(diagnostics, diagnosticUris, repositoryRoot, localResult);
        const localCount = localResult.findings.length;
        const provider = (0, providers_1.providerByID)(configuration.provider);
        const shouldRunAI = configuration.aiEnabled
            && provider !== undefined
            && (interactive || configuration.aiAutoReview);
        if (!shouldRunAI) {
            clearAIView(reviewTree, treeView, stagedContent);
            setFindingStatus(status, localCount);
            return;
        }
        let apiKey = await context.secrets.get(provider.secretKey);
        if ((apiKey === undefined || apiKey.trim() === "") && interactive) {
            apiKey = await getOrPromptForAPIKey(context.secrets, provider.id);
        }
        const approved = interactive
            ? await approveAIEgress(context.workspaceState, repositoryRoot, provider.id, configuration.model)
            : isAIEgressApproved(context.workspaceState, repositoryRoot, provider.id, configuration.model);
        if (apiKey === undefined || apiKey.trim() === "" || configuration.model === "" || !approved) {
            clearAIView(reviewTree, treeView, stagedContent);
            setFindingStatus(status, localCount);
            return;
        }
        setStatus(status, `Code Review: ${localCount} local issue${localCount === 1 ? "" : "s"} · AI reviewing…`);
        try {
            const aiResult = await runReviewPhase(repositoryRoot, snapshot.reviewId, configuration, apiKey, signal);
            await requireCurrentSnapshot(repositoryRoot, configuration.executable, snapshot.reviewId, signal);
            publishDiagnostics(diagnostics, diagnosticUris, repositoryRoot, aiResult);
            const aiReview = (0, aiReview_1.selectAIReview)(aiResult, reviewedFiles(aiResult));
            if (aiReview !== undefined) {
                currentSession = {
                    environment: (0, environment_1.buildReviewerEnvironment)(process.env),
                    executable: configuration.executable,
                    repositoryRoot,
                    reviewId: snapshot.reviewId
                };
                stagedContent.clear();
                reviewTree.setReview(aiReview.files, aiReview.findings);
                treeView.description = `${aiReview.files.length} AI-reviewed files, ${aiReview.findings.length} AI comments`;
                treeView.message = aiReview.files.length === 0 ? "No files were sent to the AI provider." : undefined;
                const firstTarget = aiReview.findings.length > 0 ? reviewTree.firstTarget() : undefined;
                if (interactive && firstTarget !== undefined) {
                    await vscode.commands.executeCommand("workbench.view.scm");
                    await openStagedDiff(currentSession, firstTarget, stagedContent);
                }
            }
            setFindingStatus(status, aiResult.findings.length);
            if (aiResult.ai !== undefined && aiResult.ai.failedBatches > 0) {
                setStatus(status, `Code Review: ${aiResult.findings.length} issues · AI partial`);
            }
        }
        catch (error) {
            if (isCancellation(error, signal)) {
                throw error;
            }
            output.error(`AI review failed for ${repositoryRoot}: ${displayError(error)}`);
            setStatus(status, `Code Review: ${localCount} issues · AI unavailable`);
        }
    }, (_repository, schedulerStatus) => updateSchedulerStatus(status, schedulerStatus, paused), (repository, error) => output.error(`Automatic review failed for ${repository}: ${displayError(error)}`));
    context.subscriptions.push(diagnostics, output, scheduler, treeView, status, vscode.workspace.registerTextDocumentContentProvider(reviewView_1.baseContentScheme, stagedContent), vscode.workspace.registerTextDocumentContentProvider(reviewView_1.indexContentScheme, stagedContent), vscode.commands.registerCommand(configureProviderCommand, async () => {
        const repositoryRoot = await activeRepositoryRoot();
        await configureAIProvider(context, repositoryRoot);
        knownRepositories.add(repositoryRoot);
        scheduler.notify(repositoryRoot, 300);
    }), vscode.commands.registerCommand(setKeyCommand, async () => {
        await setProviderAPIKey(context.secrets, "openai");
        notifyActiveRepository(scheduler, knownRepositories);
    }), vscode.commands.registerCommand(clearKeyCommand, async () => {
        await clearProviderAPIKey(context.secrets, "openai");
        notifyActiveRepository(scheduler, knownRepositories);
    }), vscode.commands.registerCommand(pauseCommand, async () => {
        paused = true;
        await context.globalState.update("autoReviewPaused", true);
        setStatus(status, "Code Review: Paused");
    }), vscode.commands.registerCommand(resumeCommand, async () => {
        paused = false;
        await context.globalState.update("autoReviewPaused", false);
        setStatus(status, "Code Review: Waiting…");
        for (const repository of knownRepositories) {
            const configuration = await readConfiguration(context, repository);
            if (configuration.autoReview) {
                scheduler.notify(repository, configuration.debounceMs);
            }
        }
    }), vscode.commands.registerCommand(reviewView_1.openDiffCommand, async (target) => {
        if (target === undefined || currentSession === undefined) {
            return;
        }
        await openStagedDiff(currentSession, target, stagedContent);
    }), vscode.commands.registerCommand(reviewCommand, async () => {
        const repositoryRoot = await activeRepositoryRoot();
        knownRepositories.add(repositoryRoot);
        interactiveRepositories.add(repositoryRoot);
        try {
            await scheduler.runNow(repositoryRoot);
        }
        finally {
            interactiveRepositories.delete(repositoryRoot);
        }
    }), vscode.workspace.onDidChangeConfiguration(async (event) => {
        if (!event.affectsConfiguration("codeReview")) {
            return;
        }
        for (const repository of knownRepositories) {
            const configuration = await readConfiguration(context, repository);
            if (!paused && configuration.autoReview) {
                scheduler.notify(repository, configuration.debounceMs);
            }
        }
    }));
    const gitWatcher = await (0, gitApi_1.watchGitRepositories)(root => {
        const repositoryRoot = root.fsPath;
        knownRepositories.add(repositoryRoot);
        if (paused) {
            return;
        }
        const configuration = vscode.workspace.getConfiguration("codeReview", root);
        if (configuration.get("autoReview", true)) {
            scheduler.notify(repositoryRoot, debounce(configuration.get("debounceMs", 500)));
        }
    });
    context.subscriptions.push(gitWatcher);
}
function deactivate() {
    for (const child of activeChildren) {
        child.kill();
    }
    activeChildren.clear();
}
async function readConfiguration(context, repositoryRoot) {
    const uri = vscode.Uri.file(repositoryRoot);
    const configuration = vscode.workspace.getConfiguration("codeReview", uri);
    return {
        aiAutoReview: configuration.get("ai.autoReview", true),
        aiEnabled: configuration.get("ai.enabled", false),
        autoReview: configuration.get("autoReview", true),
        debounceMs: debounce(configuration.get("debounceMs", 500)),
        excludes: configuration.get("exclude", []),
        executable: reviewerExecutable(configuration.get("binaryPath")?.trim(), repositoryRoot, context.extensionPath),
        model: configuration.get("model", "").trim(),
        provider: configuration.get("provider", "none").trim()
    };
}
function debounce(value) {
    return Math.max(300, Math.min(5000, value));
}
async function readSnapshot(repositoryRoot, executable, signal) {
    return withCancellation(signal, async (token) => (0, protocol_1.parseSnapshotResult)(await runProcess(executable, ["snapshot", "--staged", "--repo", repositoryRoot], repositoryRoot, (0, environment_1.buildReviewerEnvironment)(process.env), token, 64 * 1024)));
}
async function runReviewPhase(repositoryRoot, reviewId, configuration, apiKey, signal) {
    const args = [
        "review",
        "--staged",
        "--format",
        "json",
        "--repo",
        repositoryRoot,
        "--expected-review-id",
        reviewId
    ];
    for (const exclude of configuration.excludes) {
        args.push("--exclude", exclude);
    }
    const environment = (0, environment_1.buildReviewerEnvironment)(process.env);
    const provider = (0, providers_1.providerByID)(configuration.provider);
    if (apiKey !== undefined && provider !== undefined) {
        environment[provider.environmentVariable] = apiKey;
        args.push("--ai-provider", configuration.provider, "--ai-model", configuration.model);
    }
    const result = await withCancellation(signal, async (token) => (0, protocol_1.parseReviewResult)(await runProcess(configuration.executable, args, repositoryRoot, environment, token, maxOutputBytes)));
    if (result.reviewId !== reviewId) {
        throw abortError("review result belongs to a stale staged snapshot");
    }
    return result;
}
async function requireCurrentSnapshot(repositoryRoot, executable, reviewId, signal) {
    const current = await readSnapshot(repositoryRoot, executable, signal);
    if (current.reviewId !== reviewId) {
        throw abortError("staged snapshot changed while review was running");
    }
}
function reviewedFiles(result) {
    return result.files.map(file => ({
        path: file.path,
        previousPath: file.previousPath,
        status: file.status
    }));
}
async function activeRepositoryRoot() {
    const folder = selectWorkspaceFolder();
    const environment = (0, environment_1.buildReviewerEnvironment)(process.env);
    const source = new vscode.CancellationTokenSource();
    try {
        const root = (await runProcess("git", ["-C", folder.uri.fsPath, "rev-parse", "--show-toplevel"], folder.uri.fsPath, environment, source.token, 64 * 1024)).trim();
        if (root === "" || !path.isAbsolute(root)) {
            throw new Error("Git returned an invalid repository root");
        }
        return root;
    }
    finally {
        source.dispose();
    }
}
function selectWorkspaceFolder() {
    const folders = vscode.workspace.workspaceFolders?.filter(folder => folder.uri.scheme === "file") ?? [];
    if (folders.length === 0) {
        throw new Error("Open a local workspace containing a Git repository first");
    }
    const activeURI = vscode.window.activeTextEditor?.document.uri;
    return (activeURI === undefined ? undefined : vscode.workspace.getWorkspaceFolder(activeURI)) ?? folders[0];
}
function reviewerExecutable(configured, repositoryRoot, extensionPath) {
    if (configured !== undefined && configured !== "") {
        return configured;
    }
    const executableName = process.platform === "win32" ? "reviewer.exe" : "reviewer";
    const repositoryBinary = path.join(repositoryRoot, executableName);
    if ((0, node_fs_1.existsSync)(repositoryBinary)) {
        return repositoryBinary;
    }
    const developmentBinary = path.join(extensionPath, "..", executableName);
    return (0, node_fs_1.existsSync)(developmentBinary) ? developmentBinary : executableName;
}
async function openStagedDiff(session, target, contentProvider) {
    try {
        await vscode.window.withProgress({
            location: vscode.ProgressLocation.Window,
            title: `Opening AI review diff for ${target.file.path}`,
            cancellable: true
        }, async (_progress, token) => {
            const snapshot = (0, protocol_1.parseSnapshotResult)(await runProcess(session.executable, ["snapshot", "--staged", "--repo", session.repositoryRoot], session.repositoryRoot, session.environment, token, 64 * 1024));
            if (snapshot.reviewId !== session.reviewId) {
                throw new Error("This AI review is stale; stage changes and wait for the new review");
            }
            const basePath = target.file.previousPath ?? target.file.path;
            const status = target.file.status.toLowerCase();
            const base = status.startsWith("a")
                ? ""
                : await readGitFile(session, `HEAD:${basePath}`, token);
            const staged = status.startsWith("d")
                ? ""
                : await readGitFile(session, `:${target.file.path}`, token);
            if (base.includes("\0") || staged.includes("\0")) {
                throw new Error(`Binary staged diff cannot be displayed for ${target.file.path}`);
            }
            const documents = contentProvider.add(target.file.path, base, staged);
            const line = Math.max(0, (target.line ?? 1) - 1);
            await vscode.commands.executeCommand("vscode.diff", documents.base, documents.staged, `${target.file.path} (AI Review: HEAD ↔ Staged)`, { preview: true, selection: new vscode.Range(line, 0, line, 0) });
        });
    }
    catch (error) {
        if (!(error instanceof vscode.CancellationError)) {
            void vscode.window.showWarningMessage(`Could not open AI review diff: ${displayError(error)}`);
        }
    }
}
function readGitFile(session, object, token) {
    return runProcess("git", ["-C", session.repositoryRoot, "show", object], session.repositoryRoot, session.environment, token, maxOutputBytes);
}
function publishDiagnostics(collection, diagnosticUris, repositoryRoot, result) {
    for (const uri of diagnosticUris.get(repositoryRoot)?.values() ?? []) {
        collection.delete(uri);
    }
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
    const current = new Map();
    for (const [key, entry] of grouped) {
        collection.set(entry.uri, entry.values);
        current.set(key, entry.uri);
    }
    diagnosticUris.set(repositoryRoot, current);
}
function clearRepositoryDiagnostics(collection, diagnosticUris, repositoryRoot) {
    for (const uri of diagnosticUris.get(repositoryRoot)?.values() ?? []) {
        collection.delete(uri);
    }
    diagnosticUris.delete(repositoryRoot);
}
function toDiagnostic(finding) {
    const diagnostic = new vscode.Diagnostic(new vscode.Range(finding.startLine - 1, 0, finding.endLine - 1, Number.MAX_SAFE_INTEGER), `${finding.title}\n\n${finding.message}${finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`}`, diagnosticSeverity(finding.severity));
    diagnostic.source = `Code Review: ${finding.source}${finding.agentId === undefined ? "" : `/${finding.agentId}`}`;
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
function clearAIView(reviewTree, treeView, stagedContent) {
    stagedContent.clear();
    reviewTree.setReview([], []);
    treeView.description = "Local rules only";
    treeView.message = "AI-reviewed diffs appear here when optional AI review is configured.";
}
async function configureAIProvider(context, repositoryRoot) {
    const selected = await vscode.window.showQuickPick([
        ...providers_1.providers.map(provider => ({
            label: provider.label,
            description: provider.description,
            provider
        })),
        { label: "Local review only", description: "Disable external AI requests", provider: undefined }
    ], { title: "Code Review: Choose AI Provider", placeHolder: "Provider" });
    if (selected === undefined) {
        return;
    }
    const configuration = vscode.workspace.getConfiguration("codeReview", vscode.Uri.file(repositoryRoot));
    if (selected.provider === undefined) {
        await configuration.update("provider", "none", vscode.ConfigurationTarget.Workspace);
        await configuration.update("model", "", vscode.ConfigurationTarget.Workspace);
        await configuration.update("ai.enabled", false, vscode.ConfigurationTarget.Workspace);
        return;
    }
    const provider = selected.provider;
    const modelChoice = await vscode.window.showQuickPick([
        ...provider.models.map(model => ({ label: model.label, description: model.description })),
        { label: "$(edit) Custom model…", description: "Enter another model ID" }
    ], { title: `Code Review: ${provider.label} Model`, placeHolder: provider.defaultModel });
    if (modelChoice === undefined) {
        return;
    }
    const model = modelChoice.label.startsWith("$(edit)")
        ? await vscode.window.showInputBox({
            title: `Code Review: ${provider.label} Model`,
            prompt: "Enter the provider model ID",
            value: provider.defaultModel,
            validateInput: value => value.trim() === "" ? "Model is required" : undefined
        })
        : modelChoice.label;
    if (model === undefined || model.trim() === "") {
        return;
    }
    const existingKey = await context.secrets.get(provider.secretKey);
    if (existingKey === undefined || existingKey.trim() === "") {
        const stored = await setProviderAPIKey(context.secrets, provider.id);
        if (!stored) {
            return;
        }
    }
    await configuration.update("provider", provider.id, vscode.ConfigurationTarget.Workspace);
    await configuration.update("model", model.trim(), vscode.ConfigurationTarget.Workspace);
    await configuration.update("ai.enabled", true, vscode.ConfigurationTarget.Workspace);
    await configuration.update("ai.autoReview", true, vscode.ConfigurationTarget.Workspace);
    await context.workspaceState.update(approvalKey(repositoryRoot), `${provider.id}:${model.trim()}`);
    void vscode.window.showInformationMessage(`${provider.label} configured for automatic code review.`);
}
async function setProviderAPIKey(secrets, providerID) {
    const provider = (0, providers_1.providerByID)(providerID);
    if (provider === undefined) {
        return false;
    }
    const value = await vscode.window.showInputBox({
        title: `Code Review: ${provider.label} API Key`,
        prompt: "Stored in VS Code SecretStorage and passed only to AI-enabled reviewer processes.",
        password: true,
        ignoreFocusOut: true,
        validateInput: input => input.trim() === "" ? "API key must not be empty" : undefined
    });
    if (value === undefined) {
        return false;
    }
    await secrets.store(provider.secretKey, value.trim());
    return true;
}
async function clearProviderAPIKey(secrets, providerID) {
    const provider = (0, providers_1.providerByID)(providerID);
    if (provider !== undefined) {
        await secrets.delete(provider.secretKey);
    }
}
async function getOrPromptForAPIKey(secrets, providerID) {
    const provider = (0, providers_1.providerByID)(providerID);
    if (provider === undefined) {
        return undefined;
    }
    const stored = await secrets.get(provider.secretKey);
    if (stored !== undefined && stored.trim() !== "") {
        return stored;
    }
    const selected = await vscode.window.showWarningMessage("AI review is not configured.", "Set API Key");
    if (selected !== "Set API Key") {
        return undefined;
    }
    await setProviderAPIKey(secrets, provider.id);
    return secrets.get(provider.secretKey);
}
async function approveAIEgress(state, repositoryRoot, providerID, model) {
    if (isAIEgressApproved(state, repositoryRoot, providerID, model)) {
        return true;
    }
    const selected = await vscode.window.showWarningMessage(`Send eligible staged changes to ${(0, providers_1.providerByID)(providerID)?.label ?? providerID} model ${model}? Environment files are excluded and secrets are redacted locally.`, { modal: true }, "Allow AI Review");
    if (selected !== "Allow AI Review") {
        return false;
    }
    await state.update(approvalKey(repositoryRoot), `${providerID}:${model}`);
    return true;
}
function isAIEgressApproved(state, repositoryRoot, providerID, model) {
    return state.get(approvalKey(repositoryRoot)) === `${providerID}:${model}`;
}
function approvalKey(repositoryRoot) {
    return `aiEgressApproval:${vscode.Uri.file(repositoryRoot).toString()}`;
}
function setStatus(status, text) {
    status.text = text;
    status.tooltip = text;
}
function setFindingStatus(status, count) {
    setStatus(status, count === 0 ? "Code Review: $(check) Clean" : `Code Review: ${count} issues`);
}
function updateSchedulerStatus(status, state, paused) {
    if (paused) {
        return;
    }
    switch (state) {
        case "scheduled":
            setStatus(status, "Code Review: Waiting…");
            break;
        case "reviewing":
            setStatus(status, "Code Review: Reviewing…");
            break;
        case "error":
            setStatus(status, "Code Review: Review failed");
            break;
        case "idle":
        case "completed":
            break;
    }
}
function notifyActiveRepository(scheduler, repositories) {
    for (const repository of repositories) {
        scheduler.notify(repository, 300);
    }
}
async function withCancellation(signal, action) {
    const source = new vscode.CancellationTokenSource();
    const cancel = () => source.cancel();
    signal.addEventListener("abort", cancel, { once: true });
    if (signal.aborted) {
        source.cancel();
    }
    try {
        return await action(source.token);
    }
    finally {
        signal.removeEventListener("abort", cancel);
        source.dispose();
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
        let forceKill;
        const finish = (action) => {
            if (settled) {
                return;
            }
            settled = true;
            activeChildren.delete(child);
            cancellation?.dispose();
            if (forceKill !== undefined) {
                clearTimeout(forceKill);
            }
            action();
        };
        const stop = (error) => {
            if (stopError !== undefined) {
                return;
            }
            stopError = error;
            child.kill("SIGTERM");
            forceKill = setTimeout(() => child.kill("SIGKILL"), 5000);
            forceKill.unref();
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
function isCancellation(error, signal) {
    return signal.aborted || error instanceof vscode.CancellationError || (error instanceof Error && error.name === "AbortError");
}
function abortError(message) {
    const error = new Error(message);
    error.name = "AbortError";
    return error;
}
function displayError(error) {
    return (error instanceof Error ? error.message : String(error)).replace(/[\p{Cc}\p{Cf}]/gu, " ").slice(0, 1000);
}
