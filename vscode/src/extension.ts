import { ChildProcess, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";

import { selectAIReview } from "./aiReview";
import { AutoReviewScheduler, ScheduledSnapshot, SchedulerStatus } from "./autoReview";
import { buildReviewerEnvironment } from "./environment";
import { watchGitRepositories } from "./gitApi";
import { resolveFindingPath } from "./paths";
import {
  parseReviewResult,
  parseSnapshotResult,
  ReviewFinding,
  ReviewResult,
  SnapshotResult
} from "./protocol";
import {
  baseContentScheme,
  indexContentScheme,
  OpenDiffTarget,
  openDiffCommand,
  reviewViewID,
  ReviewTreeProvider,
  StagedContentProvider
} from "./reviewView";
import { StagedFile } from "./stagedFiles";

const reviewCommand = "code-review.reviewStaged";
const setKeyCommand = "code-review.setOpenAIAPIKey";
const clearKeyCommand = "code-review.clearOpenAIAPIKey";
const pauseCommand = "code-review.pauseAutoReview";
const resumeCommand = "code-review.resumeAutoReview";
const openAIKeySecret = "code-review.openaiApiKey";
const openAIKeyEnvironment = "REVIEWER_OPENAI_API_KEY";
const maxOutputBytes = 32 * 1024 * 1024;
const maxErrorBytes = 1024 * 1024;
const activeChildren = new Set<ChildProcess>();

interface ReviewSession {
  environment: NodeJS.ProcessEnv;
  executable: string;
  repositoryRoot: string;
  reviewId: string;
}

interface ReviewConfiguration {
  aiAutoReview: boolean;
  aiEnabled: boolean;
  autoReview: boolean;
  debounceMs: number;
  excludes: string[];
  executable: string;
  model: string;
  provider: string;
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  const diagnostics = vscode.languages.createDiagnosticCollection("code-review");
  const diagnosticUris = new Map<string, Map<string, vscode.Uri>>();
  const reviewTree = new ReviewTreeProvider();
  const treeView = vscode.window.createTreeView(reviewViewID, { treeDataProvider: reviewTree });
  const stagedContent = new StagedContentProvider();
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
  const output = vscode.window.createOutputChannel("Code Review", { log: true });
  const knownRepositories = new Set<string>();
  const interactiveRepositories = new Set<string>();
  let currentSession: ReviewSession | undefined;
  let paused = context.globalState.get<boolean>("autoReviewPaused", false);

  status.name = "Code Review";
  status.command = "workbench.actions.view.problems";
  setStatus(status, paused ? "Code Review: Paused" : "Code Review");
  status.show();

  const scheduler = new AutoReviewScheduler(
    500,
    async (repositoryRoot, signal) => {
      const configuration = await readConfiguration(context, repositoryRoot);
      const snapshot = await readSnapshot(repositoryRoot, configuration.executable, signal);
      const hasKey = (await context.secrets.get(openAIKeySecret))?.trim() !== "";
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
    },
    async (repositoryRoot, snapshot, signal) => {
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
      const shouldRunAI = configuration.aiEnabled
        && configuration.provider === "openai"
        && (interactive || configuration.aiAutoReview);
      if (!shouldRunAI) {
        clearAIView(reviewTree, treeView, stagedContent);
        setFindingStatus(status, localCount);
        return;
      }

      let apiKey = await context.secrets.get(openAIKeySecret);
      if ((apiKey === undefined || apiKey.trim() === "") && interactive) {
        apiKey = await getOrPromptForOpenAIKey(context.secrets);
      }
      const approved = interactive
        ? await approveAIEgress(context.workspaceState, repositoryRoot, configuration.model)
        : isAIEgressApproved(context.workspaceState, repositoryRoot, configuration.model);
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
        const aiReview = selectAIReview(aiResult, reviewedFiles(aiResult));
        if (aiReview !== undefined) {
          currentSession = {
            environment: buildReviewerEnvironment(process.env),
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
      } catch (error) {
        if (isCancellation(error, signal)) {
          throw error;
        }
        output.error(`AI review failed for ${repositoryRoot}: ${displayError(error)}`);
        setStatus(status, `Code Review: ${localCount} issues · AI unavailable`);
      }
    },
    (_repository, schedulerStatus) => updateSchedulerStatus(status, schedulerStatus, paused),
    (repository, error) => output.error(`Automatic review failed for ${repository}: ${displayError(error)}`)
  );

  context.subscriptions.push(
    diagnostics,
    output,
    scheduler,
    treeView,
    status,
    vscode.workspace.registerTextDocumentContentProvider(baseContentScheme, stagedContent),
    vscode.workspace.registerTextDocumentContentProvider(indexContentScheme, stagedContent),
    vscode.commands.registerCommand(setKeyCommand, async () => {
      await setOpenAIKey(context.secrets);
      notifyActiveRepository(scheduler, knownRepositories);
    }),
    vscode.commands.registerCommand(clearKeyCommand, async () => {
      await clearOpenAIKey(context.secrets);
      notifyActiveRepository(scheduler, knownRepositories);
    }),
    vscode.commands.registerCommand(pauseCommand, async () => {
      paused = true;
      await context.globalState.update("autoReviewPaused", true);
      setStatus(status, "Code Review: Paused");
    }),
    vscode.commands.registerCommand(resumeCommand, async () => {
      paused = false;
      await context.globalState.update("autoReviewPaused", false);
      setStatus(status, "Code Review: Waiting…");
      for (const repository of knownRepositories) {
        const configuration = await readConfiguration(context, repository);
        if (configuration.autoReview) {
          scheduler.notify(repository, configuration.debounceMs);
        }
      }
    }),
    vscode.commands.registerCommand(openDiffCommand, async (target?: OpenDiffTarget) => {
      if (target === undefined || currentSession === undefined) {
        return;
      }
      await openStagedDiff(currentSession, target, stagedContent);
    }),
    vscode.commands.registerCommand(reviewCommand, async () => {
      const repositoryRoot = await activeRepositoryRoot();
      knownRepositories.add(repositoryRoot);
      interactiveRepositories.add(repositoryRoot);
      try {
        await scheduler.runNow(repositoryRoot);
      } finally {
        interactiveRepositories.delete(repositoryRoot);
      }
    }),
    vscode.workspace.onDidChangeConfiguration(async event => {
      if (!event.affectsConfiguration("codeReview")) {
        return;
      }
      for (const repository of knownRepositories) {
        const configuration = await readConfiguration(context, repository);
        if (!paused && configuration.autoReview) {
          scheduler.notify(repository, configuration.debounceMs);
        }
      }
    })
  );

  const gitWatcher = await watchGitRepositories(root => {
    const repositoryRoot = root.fsPath;
    knownRepositories.add(repositoryRoot);
    if (paused) {
      return;
    }
    const configuration = vscode.workspace.getConfiguration("codeReview", root);
    if (configuration.get<boolean>("autoReview", true)) {
      scheduler.notify(repositoryRoot, debounce(configuration.get<number>("debounceMs", 500)));
    }
  });
  context.subscriptions.push(gitWatcher);
}

export function deactivate(): void {
  for (const child of activeChildren) {
    child.kill();
  }
  activeChildren.clear();
}

async function readConfiguration(context: vscode.ExtensionContext, repositoryRoot: string): Promise<ReviewConfiguration> {
  const uri = vscode.Uri.file(repositoryRoot);
  const configuration = vscode.workspace.getConfiguration("codeReview", uri);
  return {
    aiAutoReview: configuration.get<boolean>("ai.autoReview", true),
    aiEnabled: configuration.get<boolean>("ai.enabled", false),
    autoReview: configuration.get<boolean>("autoReview", true),
    debounceMs: debounce(configuration.get<number>("debounceMs", 500)),
    excludes: configuration.get<string[]>("exclude", []),
    executable: reviewerExecutable(configuration.get<string>("binaryPath")?.trim(), repositoryRoot, context.extensionPath),
    model: configuration.get<string>("model", "").trim(),
    provider: configuration.get<string>("provider", "none").trim()
  };
}

function debounce(value: number): number {
  return Math.max(300, Math.min(5000, value));
}

async function readSnapshot(repositoryRoot: string, executable: string, signal: AbortSignal): Promise<SnapshotResult> {
  return withCancellation(signal, async token => parseSnapshotResult(await runProcess(
    executable,
    ["snapshot", "--staged", "--repo", repositoryRoot],
    repositoryRoot,
    buildReviewerEnvironment(process.env),
    token,
    64 * 1024
  )));
}

async function runReviewPhase(
  repositoryRoot: string,
  reviewId: string,
  configuration: ReviewConfiguration,
  apiKey: string | undefined,
  signal: AbortSignal
): Promise<ReviewResult> {
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
  const environment = buildReviewerEnvironment(process.env);
  if (apiKey !== undefined) {
    environment[openAIKeyEnvironment] = apiKey;
    args.push("--ai-provider", configuration.provider, "--ai-model", configuration.model);
  }
  const result = await withCancellation(signal, async token => parseReviewResult(await runProcess(
    configuration.executable,
    args,
    repositoryRoot,
    environment,
    token,
    maxOutputBytes
  )));
  if (result.reviewId !== reviewId) {
    throw abortError("review result belongs to a stale staged snapshot");
  }
  return result;
}

async function requireCurrentSnapshot(
  repositoryRoot: string,
  executable: string,
  reviewId: string,
  signal: AbortSignal
): Promise<void> {
  const current = await readSnapshot(repositoryRoot, executable, signal);
  if (current.reviewId !== reviewId) {
    throw abortError("staged snapshot changed while review was running");
  }
}

function reviewedFiles(result: ReviewResult): StagedFile[] {
  return result.files.map(file => ({
    path: file.path,
    previousPath: file.previousPath,
    status: file.status
  }));
}

async function activeRepositoryRoot(): Promise<string> {
  const folder = selectWorkspaceFolder();
  const environment = buildReviewerEnvironment(process.env);
  const source = new vscode.CancellationTokenSource();
  try {
    const root = (await runProcess(
      "git",
      ["-C", folder.uri.fsPath, "rev-parse", "--show-toplevel"],
      folder.uri.fsPath,
      environment,
      source.token,
      64 * 1024
    )).trim();
    if (root === "" || !path.isAbsolute(root)) {
      throw new Error("Git returned an invalid repository root");
    }
    return root;
  } finally {
    source.dispose();
  }
}

function selectWorkspaceFolder(): vscode.WorkspaceFolder {
  const folders = vscode.workspace.workspaceFolders?.filter(folder => folder.uri.scheme === "file") ?? [];
  if (folders.length === 0) {
    throw new Error("Open a local workspace containing a Git repository first");
  }
  const activeURI = vscode.window.activeTextEditor?.document.uri;
  return (activeURI === undefined ? undefined : vscode.workspace.getWorkspaceFolder(activeURI)) ?? folders[0];
}

function reviewerExecutable(configured: string | undefined, repositoryRoot: string, extensionPath: string): string {
  if (configured !== undefined && configured !== "") {
    return configured;
  }
  const executableName = process.platform === "win32" ? "reviewer.exe" : "reviewer";
  const repositoryBinary = path.join(repositoryRoot, executableName);
  if (existsSync(repositoryBinary)) {
    return repositoryBinary;
  }
  const developmentBinary = path.join(extensionPath, "..", executableName);
  return existsSync(developmentBinary) ? developmentBinary : executableName;
}

async function openStagedDiff(
  session: ReviewSession,
  target: OpenDiffTarget,
  contentProvider: StagedContentProvider
): Promise<void> {
  try {
    await vscode.window.withProgress(
      {
        location: vscode.ProgressLocation.Window,
        title: `Opening AI review diff for ${target.file.path}`,
        cancellable: true
      },
      async (_progress, token) => {
        const snapshot = parseSnapshotResult(await runProcess(
          session.executable,
          ["snapshot", "--staged", "--repo", session.repositoryRoot],
          session.repositoryRoot,
          session.environment,
          token,
          64 * 1024
        ));
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
        await vscode.commands.executeCommand(
          "vscode.diff",
          documents.base,
          documents.staged,
          `${target.file.path} (AI Review: HEAD ↔ Staged)`,
          { preview: true, selection: new vscode.Range(line, 0, line, 0) }
        );
      }
    );
  } catch (error) {
    if (!(error instanceof vscode.CancellationError)) {
      void vscode.window.showWarningMessage(`Could not open AI review diff: ${displayError(error)}`);
    }
  }
}

function readGitFile(session: ReviewSession, object: string, token: vscode.CancellationToken): Promise<string> {
  return runProcess(
    "git",
    ["-C", session.repositoryRoot, "show", object],
    session.repositoryRoot,
    session.environment,
    token,
    maxOutputBytes
  );
}

function publishDiagnostics(
  collection: vscode.DiagnosticCollection,
  diagnosticUris: Map<string, Map<string, vscode.Uri>>,
  repositoryRoot: string,
  result: ReviewResult
): void {
  for (const uri of diagnosticUris.get(repositoryRoot)?.values() ?? []) {
    collection.delete(uri);
  }
  const grouped = new Map<string, { uri: vscode.Uri; values: vscode.Diagnostic[] }>();
  for (const finding of result.findings) {
    const filePath = resolveFindingPath(repositoryRoot, finding.file);
    if (filePath === undefined) {
      continue;
    }
    const uri = vscode.Uri.file(filePath);
    const key = uri.toString();
    const entry = grouped.get(key) ?? { uri, values: [] };
    entry.values.push(toDiagnostic(finding));
    grouped.set(key, entry);
  }
  const current = new Map<string, vscode.Uri>();
  for (const [key, entry] of grouped) {
    collection.set(entry.uri, entry.values);
    current.set(key, entry.uri);
  }
  diagnosticUris.set(repositoryRoot, current);
}

function clearRepositoryDiagnostics(
  collection: vscode.DiagnosticCollection,
  diagnosticUris: Map<string, Map<string, vscode.Uri>>,
  repositoryRoot: string
): void {
  for (const uri of diagnosticUris.get(repositoryRoot)?.values() ?? []) {
    collection.delete(uri);
  }
  diagnosticUris.delete(repositoryRoot);
}

function toDiagnostic(finding: ReviewFinding): vscode.Diagnostic {
  const diagnostic = new vscode.Diagnostic(
    new vscode.Range(finding.startLine - 1, 0, finding.endLine - 1, Number.MAX_SAFE_INTEGER),
    `${finding.title}\n\n${finding.message}${finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`}`,
    diagnosticSeverity(finding.severity)
  );
  diagnostic.source = `Code Review: ${finding.source}${finding.agentId === undefined ? "" : `/${finding.agentId}`}`;
  diagnostic.code = finding.category;
  return diagnostic;
}

function diagnosticSeverity(severity: ReviewFinding["severity"]): vscode.DiagnosticSeverity {
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

function clearAIView(
  reviewTree: ReviewTreeProvider,
  treeView: vscode.TreeView<unknown>,
  stagedContent: StagedContentProvider
): void {
  stagedContent.clear();
  reviewTree.setReview([], []);
  treeView.description = "Local rules only";
  treeView.message = "AI-reviewed diffs appear here when optional AI review is configured.";
}

async function setOpenAIKey(secrets: vscode.SecretStorage): Promise<void> {
  const value = await vscode.window.showInputBox({
    title: "Code Review: OpenAI API Key",
    prompt: "Stored in VS Code SecretStorage and passed only to AI-enabled reviewer processes.",
    password: true,
    ignoreFocusOut: true,
    validateInput: input => input.trim() === "" ? "API key must not be empty" : undefined
  });
  if (value !== undefined) {
    await secrets.store(openAIKeySecret, value.trim());
  }
}

async function clearOpenAIKey(secrets: vscode.SecretStorage): Promise<void> {
  await secrets.delete(openAIKeySecret);
}

async function getOrPromptForOpenAIKey(secrets: vscode.SecretStorage): Promise<string | undefined> {
  const stored = await secrets.get(openAIKeySecret);
  if (stored !== undefined && stored.trim() !== "") {
    return stored;
  }
  const selected = await vscode.window.showWarningMessage(
    "AI review is not configured.",
    "Set API Key"
  );
  if (selected !== "Set API Key") {
    return undefined;
  }
  await setOpenAIKey(secrets);
  return secrets.get(openAIKeySecret);
}

async function approveAIEgress(
  state: vscode.Memento,
  repositoryRoot: string,
  model: string
): Promise<boolean> {
  if (isAIEgressApproved(state, repositoryRoot, model)) {
    return true;
  }
  const selected = await vscode.window.showWarningMessage(
    `Send eligible staged changes to OpenAI model ${model}? Environment files are excluded and secrets are redacted locally.`,
    { modal: true },
    "Allow AI Review"
  );
  if (selected !== "Allow AI Review") {
    return false;
  }
  await state.update(approvalKey(repositoryRoot), `openai:${model}`);
  return true;
}

function isAIEgressApproved(state: vscode.Memento, repositoryRoot: string, model: string): boolean {
  return state.get<string>(approvalKey(repositoryRoot)) === `openai:${model}`;
}

function approvalKey(repositoryRoot: string): string {
  return `aiEgressApproval:${vscode.Uri.file(repositoryRoot).toString()}`;
}

function setStatus(status: vscode.StatusBarItem, text: string): void {
  status.text = text;
  status.tooltip = text;
}

function setFindingStatus(status: vscode.StatusBarItem, count: number): void {
  setStatus(status, count === 0 ? "Code Review: $(check) Clean" : `Code Review: ${count} issues`);
}

function updateSchedulerStatus(status: vscode.StatusBarItem, state: SchedulerStatus, paused: boolean): void {
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

function notifyActiveRepository(scheduler: AutoReviewScheduler, repositories: Set<string>): void {
  for (const repository of repositories) {
    scheduler.notify(repository, 300);
  }
}

async function withCancellation<T>(
  signal: AbortSignal,
  action: (token: vscode.CancellationToken) => Promise<T>
): Promise<T> {
  const source = new vscode.CancellationTokenSource();
  const cancel = (): void => source.cancel();
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) {
    source.cancel();
  }
  try {
    return await action(source.token);
  } finally {
    signal.removeEventListener("abort", cancel);
    source.dispose();
  }
}

function runProcess(
  executable: string,
  args: string[],
  cwd: string,
  environment: NodeJS.ProcessEnv,
  token: vscode.CancellationToken,
  stdoutLimit: number
): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {
      cwd,
      env: environment,
      shell: false,
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"]
    });
    activeChildren.add(child);
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let settled = false;
    let stopError: Error | undefined;
    let cancellation: vscode.Disposable | undefined;
    let forceKill: NodeJS.Timeout | undefined;

    const finish = (action: () => void): void => {
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
    const stop = (error: Error): void => {
      if (stopError !== undefined) {
        return;
      }
      stopError = error;
      child.kill("SIGTERM");
      forceKill = setTimeout(() => child.kill("SIGKILL"), 5000);
      forceKill.unref();
    };
    child.stdout.on("data", (chunk: Buffer) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > stdoutLimit) {
        stop(new Error(`${executable} output exceeded the safe limit`));
        return;
      }
      stdout.push(chunk);
    });
    child.stderr.on("data", (chunk: Buffer) => {
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

function executableError(executable: string, error: Error): Error {
  if ((error as NodeJS.ErrnoException).code === "ENOENT") {
    return new Error(`Executable ${JSON.stringify(executable)} was not found; configure codeReview.binaryPath`);
  }
  return error;
}

function isCancellation(error: unknown, signal: AbortSignal): boolean {
  return signal.aborted || error instanceof vscode.CancellationError || (error instanceof Error && error.name === "AbortError");
}

function abortError(message: string): Error {
  const error = new Error(message);
  error.name = "AbortError";
  return error;
}

function displayError(error: unknown): string {
  return (error instanceof Error ? error.message : String(error)).replace(/[\p{Cc}\p{Cf}]/gu, " ").slice(0, 1000);
}
