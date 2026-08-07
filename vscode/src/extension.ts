import { ChildProcess, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";

import { configuredReviewAgentIDs, ReviewAgentID, reviewAgents, reviewAgentSummary } from "./agents";
import { selectAIReview } from "./aiReview";
import { AutoReviewScheduler, ScheduledSnapshot, SchedulerStatus } from "./autoReview";
import { AICommentPresenter } from "./comments";
import { buildReviewerEnvironment } from "./environment";
import {
  applySuggestedFixCommand,
  fixContentScheme,
  previewSuggestedFixCommand,
  SuggestedFixController,
  SuggestedFixSession
} from "./fixController";
import { watchGitRepositories } from "./gitApi";
import { resolveFindingPath } from "./paths";
import {
  configureProviderCommand,
  manageAPIKeyCommand,
  providerViewID,
  ProviderTreeProvider,
  selectAgentsCommand,
  selectModelCommand
} from "./providerView";
import { providerByID, providers } from "./providers";
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
const maxOutputBytes = 32 * 1024 * 1024;
const maxErrorBytes = 1024 * 1024;
const activeChildren = new Set<ChildProcess>();

interface ReviewSession extends SuggestedFixSession {
  environment: NodeJS.ProcessEnv;
  executable: string;
  repositoryRoot: string;
  reviewId: string;
  providerLabel: string;
}

interface ReviewConfiguration {
  aiAutoReview: boolean;
  agents: ReviewAgentID[];
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
  const providerTree = new ProviderTreeProvider();
  const providerView = vscode.window.createTreeView(providerViewID, { treeDataProvider: providerTree });
  const stagedContent = new StagedContentProvider();
  const fixController = new SuggestedFixController(
    async (session, token) => parseSnapshotResult(await runProcess(
      session.executable,
      ["snapshot", "--staged", "--repo", session.repositoryRoot],
      session.repositoryRoot,
      session.environment,
      token,
      64 * 1024
    )).reviewId,
    (session, file, token) => readGitFile(session, `:${file}`, token)
  );
  const commentPresenter = new AICommentPresenter();
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 100);
  const providerStatus = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  const output = vscode.window.createOutputChannel("Code Review", { log: true });
  const knownRepositories = new Set<string>();
  const interactiveRepositories = new Set<string>();
  let currentSession: ReviewSession | undefined;
  let paused = context.globalState.get<boolean>("autoReviewPaused", false);

  status.name = "Code Review";
  status.command = "workbench.actions.view.problems";
  setStatus(status, paused ? "Code Review: Paused" : "Code Review");
  status.show();
  providerStatus.name = "Code Review AI Provider";
  providerStatus.command = configureProviderCommand;
  providerStatus.show();

  const refreshProviderUI = async (): Promise<void> => {
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (folder === undefined) {
      providerTree.update({ agents: [], autoReview: false, enabled: false, keyStored: false, model: "" });
      providerStatus.text = "$(circle-slash) AI: Off";
      providerStatus.tooltip = "Open a workspace to configure AI review";
      return;
    }
    const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
    const provider = providerByID(configuration.get<string>("provider", "none"));
    const enabled = configuration.get<boolean>("ai.enabled", false) && provider !== undefined;
    const keyStored = provider === undefined
      ? false
      : (await context.secrets.get(provider.secretKey))?.trim() !== "";
    const model = configuration.get<string>("model", "").trim();
    const agents = configuredReviewAgentIDs(configuration.get<string[]>("ai.agents", []));
    providerTree.update({
      agents,
      autoReview: enabled && configuration.get<boolean>("ai.autoReview", true),
      enabled,
      keyStored,
      model,
      provider
    });
    if (!enabled || provider === undefined) {
      providerStatus.text = "$(circle-slash) AI: Off";
      providerStatus.tooltip = "Click to choose an AI provider";
      return;
    }
    providerStatus.text = `$(sparkle) AI: ${provider.label} · ${model || provider.defaultModel}`;
    providerStatus.tooltip = keyStored
      ? `${provider.label} is configured with ${reviewAgentSummary(agents)}. Click to change provider.`
      : `${provider.label} API key is missing. Click to configure.`;
  };
  await refreshProviderUI();

  const scheduler = new AutoReviewScheduler(
    500,
    async (repositoryRoot, signal) => {
      const configuration = await readConfiguration(context, repositoryRoot);
      const snapshot = await readSnapshot(repositoryRoot, configuration.executable, signal);
      const provider = providerByID(configuration.provider);
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
          configuration.agents.join(","),
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
      fixController.clearRepository(repositoryRoot);
      if (currentSession?.repositoryRoot === repositoryRoot) {
        currentSession = undefined;
        clearAIView(reviewTree, treeView, stagedContent, commentPresenter, repositoryRoot);
      }
      const localResult = await runReviewPhase(repositoryRoot, snapshot.reviewId, configuration, undefined, signal);
      await requireCurrentSnapshot(repositoryRoot, configuration.executable, snapshot.reviewId, signal);
      fixController.setReview({
        environment: buildReviewerEnvironment(process.env),
        executable: configuration.executable,
        repositoryRoot,
        reviewId: snapshot.reviewId
      }, localResult.findings);
      publishDiagnostics(diagnostics, diagnosticUris, repositoryRoot, localResult, fixController);

      const localCount = localResult.findings.length;
      const provider = providerByID(configuration.provider);
      const shouldRunAI = configuration.aiEnabled
        && provider !== undefined
        && (interactive || configuration.aiAutoReview);
      if (!shouldRunAI) {
        clearAIView(reviewTree, treeView, stagedContent, commentPresenter, repositoryRoot);
        setFindingStatus(status, localCount, "Local");
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
        clearAIView(reviewTree, treeView, stagedContent, commentPresenter, repositoryRoot);
        setFindingStatus(status, localCount, "Local");
        return;
      }

      setStatus(status, `Code Review [${provider.label}]: ${localCount} local · AI reviewing…`);
      try {
        const aiResult = await runReviewPhase(repositoryRoot, snapshot.reviewId, configuration, apiKey, signal);
        await requireCurrentSnapshot(repositoryRoot, configuration.executable, snapshot.reviewId, signal);
        const fixSession: SuggestedFixSession = {
          environment: buildReviewerEnvironment(process.env),
          executable: configuration.executable,
          repositoryRoot,
          reviewId: snapshot.reviewId
        };
        fixController.setReview(fixSession, aiResult.findings);
        publishDiagnostics(diagnostics, diagnosticUris, repositoryRoot, aiResult, fixController);
        const aiReview = selectAIReview(aiResult, reviewedFiles(aiResult));
        if (aiReview !== undefined) {
          commentPresenter.publishSource(repositoryRoot, aiReview.findings, provider.label);
          currentSession = {
            environment: buildReviewerEnvironment(process.env),
            executable: configuration.executable,
            repositoryRoot,
            reviewId: snapshot.reviewId,
            providerLabel: provider.label
          };
          stagedContent.clear();
          reviewTree.setReview(aiReview.files, aiReview.findings);
          treeView.description = `${provider.label} · ${aiReview.findings.length} AI comments`;
          treeView.message = aiReview.files.length === 0 ? `${provider.label} completed with no AI comments.` : undefined;
          const firstTarget = aiReview.findings.length > 0 ? reviewTree.firstTarget() : undefined;
          if (interactive && firstTarget !== undefined) {
            await vscode.commands.executeCommand("workbench.view.extension.code-review");
            await openStagedDiff(currentSession, firstTarget, stagedContent, commentPresenter);
          }
        }
        setFindingStatus(status, aiResult.findings.length, provider.label);
        if (aiResult.ai !== undefined && aiResult.ai.failedBatches > 0) {
          setStatus(status, `Code Review [${provider.label}]: ${aiResult.findings.length} issues · partial`);
        }
      } catch (error) {
        if (isCancellation(error, signal)) {
          throw error;
        }
        output.error(`AI review failed for ${repositoryRoot}: ${displayError(error)}`);
        treeView.description = provider.label;
        treeView.message = `${provider.label} is currently unavailable. Local findings remain in Problems.`;
        setStatus(status, `Code Review [${provider.label}]: ${localCount} issues · unavailable`);
      }
    },
    (_repository, schedulerStatus) => updateSchedulerStatus(status, schedulerStatus, paused),
    (repository, error) => output.error(`Automatic review failed for ${repository}: ${displayError(error)}`)
  );

  context.subscriptions.push(
    diagnostics,
    commentPresenter,
    fixController,
    providerView,
    providerStatus,
    output,
    scheduler,
    treeView,
    status,
    vscode.workspace.registerTextDocumentContentProvider(baseContentScheme, stagedContent),
    vscode.workspace.registerTextDocumentContentProvider(indexContentScheme, stagedContent),
    vscode.workspace.registerTextDocumentContentProvider(fixContentScheme, fixController),
    vscode.languages.registerCodeActionsProvider(
      { scheme: "file" },
      fixController,
      { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] }
    ),
    vscode.commands.registerCommand(previewSuggestedFixCommand, value => fixController.preview(value)),
    vscode.commands.registerCommand(applySuggestedFixCommand, value => fixController.apply(value)),
    vscode.commands.registerCommand(configureProviderCommand, async () => {
      const repositoryRoot = await activeRepositoryRoot();
      await configureAIProvider(context, repositoryRoot);
      await refreshProviderUI();
      knownRepositories.add(repositoryRoot);
      scheduler.notify(repositoryRoot, 300);
    }),
    vscode.commands.registerCommand(selectModelCommand, async () => {
      const repositoryRoot = await activeRepositoryRoot();
      await selectAIModel(context, repositoryRoot);
      await refreshProviderUI();
      scheduler.notify(repositoryRoot, 300);
    }),
    vscode.commands.registerCommand(selectAgentsCommand, async () => {
      const repositoryRoot = await activeRepositoryRoot();
      await selectAIReviewAgents(repositoryRoot);
      await refreshProviderUI();
      knownRepositories.add(repositoryRoot);
      scheduler.notify(repositoryRoot, 300);
    }),
    vscode.commands.registerCommand(manageAPIKeyCommand, async () => {
      await manageCurrentProviderAPIKey(context);
      await refreshProviderUI();
      notifyActiveRepository(scheduler, knownRepositories);
    }),
    vscode.commands.registerCommand(setKeyCommand, async () => {
      await setProviderAPIKey(context.secrets, "openai");
      await refreshProviderUI();
      notifyActiveRepository(scheduler, knownRepositories);
    }),
    vscode.commands.registerCommand(clearKeyCommand, async () => {
      await clearProviderAPIKey(context.secrets, "openai");
      await refreshProviderUI();
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
      await openStagedDiff(currentSession, target, stagedContent, commentPresenter);
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
      await refreshProviderUI();
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
    agents: configuredReviewAgentIDs(configuration.get<string[]>("ai.agents", [])),
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
  const provider = providerByID(configuration.provider);
  if (apiKey !== undefined && provider !== undefined) {
    environment[provider.environmentVariable] = apiKey;
    args.push("--ai-provider", configuration.provider, "--ai-model", configuration.model);
    for (const agent of configuration.agents) {
      args.push("--ai-agent", agent);
    }
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
  contentProvider: StagedContentProvider,
  commentPresenter: AICommentPresenter
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
        commentPresenter.showPreview(documents.staged, target.findings, session.providerLabel);
      }
    );
  } catch (error) {
    if (!(error instanceof vscode.CancellationError)) {
      void vscode.window.showWarningMessage(`Could not open AI review diff: ${displayError(error)}`);
    }
  }
}

function readGitFile(session: SuggestedFixSession, object: string, token: vscode.CancellationToken): Promise<string> {
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
  result: ReviewResult,
  fixController?: SuggestedFixController
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
    const diagnostic = toDiagnostic(finding);
    fixController?.registerDiagnostic(diagnostic, finding);
    entry.values.push(diagnostic);
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
  const suggestion = finding.suggestion === undefined || finding.suggestion.trim() === ""
    ? ""
    : `\n\nSuggested action: ${finding.suggestion}`;
  const diagnostic = new vscode.Diagnostic(
    new vscode.Range(finding.startLine - 1, 0, finding.endLine - 1, Number.MAX_SAFE_INTEGER),
    `${finding.severity.toUpperCase()} · ${finding.category}\n${finding.title}\n\n${finding.message}${suggestion}`,
    diagnosticSeverity(finding.severity)
  );
  diagnostic.source = `Code Review: ${finding.source}${finding.agentId === undefined ? "" : `/${finding.agentId}`}`;
  diagnostic.code = finding.ruleId;
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
  stagedContent: StagedContentProvider,
  commentPresenter: AICommentPresenter,
  repositoryRoot: string
): void {
  stagedContent.clear();
  commentPresenter.clearSource(repositoryRoot);
  commentPresenter.clearPreview();
  reviewTree.setReview([], []);
  treeView.description = "Local rules only";
  treeView.message = "AI-reviewed diffs appear here when optional AI review is configured.";
}

async function configureAIProvider(context: vscode.ExtensionContext, repositoryRoot: string): Promise<void> {
  const selected = await vscode.window.showQuickPick(
    [
      ...providers.map(provider => ({
        label: provider.label,
        description: provider.description,
        provider
      })),
      { label: "Local review only", description: "Disable external AI requests", provider: undefined }
    ],
    { title: "Code Review: Choose AI Provider", placeHolder: "Provider" }
  );
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
  const currentProvider = configuration.get<string>("provider", "none");
  const currentModel = configuration.get<string>("model", "").trim();
  const model = currentProvider === provider.id && currentModel !== "" ? currentModel : provider.defaultModel;
  const existingKey = await context.secrets.get(provider.secretKey);
  if (existingKey === undefined || existingKey.trim() === "") {
    const stored = await setProviderAPIKey(context.secrets, provider.id);
    if (!stored) {
      return;
    }
  }
  const approval = await vscode.window.showWarningMessage(
    `Enable automatic ${provider.label} review with ${model}? Eligible staged code will be redacted locally before it is sent.`,
    { modal: true },
    "Enable AI Review"
  );
  if (approval !== "Enable AI Review") {
    return;
  }
  await configuration.update("provider", provider.id, vscode.ConfigurationTarget.Workspace);
  await configuration.update("model", model, vscode.ConfigurationTarget.Workspace);
  await configuration.update("ai.enabled", true, vscode.ConfigurationTarget.Workspace);
  await configuration.update("ai.autoReview", true, vscode.ConfigurationTarget.Workspace);
  await context.workspaceState.update(approvalKey(repositoryRoot), `${provider.id}:${model}`);
  void vscode.window.showInformationMessage(`${provider.label} · ${model} selected for automatic review.`);
}

async function selectAIModel(context: vscode.ExtensionContext, repositoryRoot: string): Promise<void> {
  const configuration = vscode.workspace.getConfiguration("codeReview", vscode.Uri.file(repositoryRoot));
  const provider = providerByID(configuration.get<string>("provider", "none"));
  if (provider === undefined) {
    await configureAIProvider(context, repositoryRoot);
    return;
  }
  const currentModel = configuration.get<string>("model", provider.defaultModel);
  const choice = await vscode.window.showQuickPick(
    [
      ...provider.models.map(model => ({
        label: model.label,
        description: model.description,
        picked: model.label === currentModel
      })),
      { label: "$(edit) Custom model…", description: "Enter another model ID", picked: false }
    ],
    { title: `Code Review: ${provider.label} Model`, placeHolder: currentModel }
  );
  if (choice === undefined) {
    return;
  }
  const model = choice.label.startsWith("$(edit)")
    ? await vscode.window.showInputBox({
      title: `Code Review: ${provider.label} Custom Model`,
      value: currentModel,
      validateInput: value => value.trim() === "" ? "Model is required" : undefined
    })
    : choice.label;
  if (model === undefined || model.trim() === "") {
    return;
  }
  const approval = await vscode.window.showWarningMessage(
    `Use ${provider.label} model ${model.trim()} for automatic staged review?`,
    { modal: true },
    "Use Model"
  );
  if (approval !== "Use Model") {
    return;
  }
  await configuration.update("model", model.trim(), vscode.ConfigurationTarget.Workspace);
  await context.workspaceState.update(approvalKey(repositoryRoot), `${provider.id}:${model.trim()}`);
}

async function selectAIReviewAgents(repositoryRoot: string): Promise<void> {
  const configuration = vscode.workspace.getConfiguration("codeReview", vscode.Uri.file(repositoryRoot));
  const current = configuredReviewAgentIDs(configuration.get<string[]>("ai.agents", []));
  const selected = await vscode.window.showQuickPick(
    reviewAgents.map(agent => ({
      label: agent.label,
      description: agent.description,
      agent,
      picked: current.includes(agent.id)
    })),
    {
      canPickMany: true,
      placeHolder: "Choose one or more review agents",
      title: "Code Review: Select AI Agents"
    }
  );
  if (selected === undefined) {
    return;
  }
  if (selected.length === 0) {
    void vscode.window.showWarningMessage("Select at least one AI review agent.");
    return;
  }
  await configuration.update(
    "ai.agents",
    selected.map(choice => choice.agent.id),
    vscode.ConfigurationTarget.Workspace
  );
}

async function manageCurrentProviderAPIKey(context: vscode.ExtensionContext): Promise<void> {
  const folder = vscode.workspace.workspaceFolders?.[0];
  if (folder === undefined) {
    return;
  }
  const configuration = vscode.workspace.getConfiguration("codeReview", folder.uri);
  const provider = providerByID(configuration.get<string>("provider", "none"));
  if (provider === undefined) {
    void vscode.window.showInformationMessage("Choose an AI provider first.", "Configure AI Provider")
      .then(selection => selection === "Configure AI Provider"
        ? vscode.commands.executeCommand(configureProviderCommand)
        : undefined);
    return;
  }
  const stored = await context.secrets.get(provider.secretKey);
  if (stored === undefined || stored.trim() === "") {
    await setProviderAPIKey(context.secrets, provider.id);
    return;
  }
  const action = await vscode.window.showQuickPick(
    [
      { label: "Replace API key", description: `Store a new ${provider.label} key` },
      { label: "Delete API key", description: "Remove the key from SecretStorage" }
    ],
    { title: `Code Review: ${provider.label} API Key` }
  );
  if (action?.label === "Replace API key") {
    await setProviderAPIKey(context.secrets, provider.id);
  } else if (action?.label === "Delete API key") {
    await clearProviderAPIKey(context.secrets, provider.id);
  }
}

async function setProviderAPIKey(secrets: vscode.SecretStorage, providerID: string): Promise<boolean> {
  const provider = providerByID(providerID);
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

async function clearProviderAPIKey(secrets: vscode.SecretStorage, providerID: string): Promise<void> {
  const provider = providerByID(providerID);
  if (provider !== undefined) {
    await secrets.delete(provider.secretKey);
  }
}

async function getOrPromptForAPIKey(secrets: vscode.SecretStorage, providerID: string): Promise<string | undefined> {
  const provider = providerByID(providerID);
  if (provider === undefined) {
    return undefined;
  }
  const stored = await secrets.get(provider.secretKey);
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
  await setProviderAPIKey(secrets, provider.id);
  return secrets.get(provider.secretKey);
}

async function approveAIEgress(
  state: vscode.Memento,
  repositoryRoot: string,
  providerID: string,
  model: string
): Promise<boolean> {
  if (isAIEgressApproved(state, repositoryRoot, providerID, model)) {
    return true;
  }
  const selected = await vscode.window.showWarningMessage(
    `Send eligible staged changes to ${providerByID(providerID)?.label ?? providerID} model ${model}? Environment files are excluded and secrets are redacted locally.`,
    { modal: true },
    "Allow AI Review"
  );
  if (selected !== "Allow AI Review") {
    return false;
  }
  await state.update(approvalKey(repositoryRoot), `${providerID}:${model}`);
  return true;
}

function isAIEgressApproved(state: vscode.Memento, repositoryRoot: string, providerID: string, model: string): boolean {
  return state.get<string>(approvalKey(repositoryRoot)) === `${providerID}:${model}`;
}

function approvalKey(repositoryRoot: string): string {
  return `aiEgressApproval:${vscode.Uri.file(repositoryRoot).toString()}`;
}

function setStatus(status: vscode.StatusBarItem, text: string): void {
  status.text = text;
  status.tooltip = text;
}

function setFindingStatus(status: vscode.StatusBarItem, count: number, mode: string): void {
  setStatus(status, count === 0 ? `Code Review [${mode}]: $(check) Clean` : `Code Review [${mode}]: ${count} issues`);
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
