import { ChildProcess, spawn } from "node:child_process";
import { realpath } from "node:fs/promises";
import * as path from "node:path";
import * as vscode from "vscode";

import { buildReviewerEnvironment } from "./environment";
import { resolveFindingPath } from "./paths";
import { FindingSeverity, parseReviewResult, ReviewFinding, ReviewResult } from "./protocol";

const configurationSection = "localCodeReviewer";
const reviewCommand = "localCodeReviewer.reviewStaged";
const setKeyCommand = "localCodeReviewer.setOpenAIAPIKey";
const clearKeyCommand = "localCodeReviewer.clearOpenAIAPIKey";
const openAIKeySecret = "localCodeReviewer.openaiApiKey";
const openAIKeyEnvironment = "REVIEWER_OPENAI_API_KEY";
const maxStdoutBytes = 32 * 1024 * 1024;
const maxStderrBytes = 1024 * 1024;
const activeChildren = new Set<ChildProcess>();

export function activate(context: vscode.ExtensionContext): void {
  const diagnostics = vscode.languages.createDiagnosticCollection("local-code-reviewer");
  const diagnosticUris = new Map<string, Map<string, vscode.Uri>>();
  let reviewRunning = false;

  context.subscriptions.push(
    diagnostics,
    vscode.commands.registerCommand(setKeyCommand, () => setOpenAIKey(context.secrets)),
    vscode.commands.registerCommand(clearKeyCommand, () => clearOpenAIKey(context.secrets)),
    vscode.commands.registerCommand(reviewCommand, async () => {
      if (reviewRunning) {
        void vscode.window.showInformationMessage("A staged review is already running.");
        return;
      }
      reviewRunning = true;
      try {
        await reviewStaged(context, diagnostics, diagnosticUris);
      } catch (error) {
        if (!(error instanceof vscode.CancellationError)) {
          void vscode.window.showErrorMessage(`Staged review failed: ${errorMessage(error)}`);
        }
      } finally {
        reviewRunning = false;
      }
    })
  );
}

export function deactivate(): void {
  for (const child of activeChildren) {
    child.kill();
  }
  activeChildren.clear();
}

async function reviewStaged(
  context: vscode.ExtensionContext,
  diagnostics: vscode.DiagnosticCollection,
  diagnosticUris: Map<string, Map<string, vscode.Uri>>
): Promise<void> {
  const folder = await selectWorkspaceFolder();
  if (folder === undefined) {
    return;
  }
  const configuration = vscode.workspace.getConfiguration(configurationSection, folder.uri);
  const binaryPath = configuration.get<string>("binaryPath", "reviewer").trim();
  if (binaryPath === "") {
    throw new Error("localCodeReviewer.binaryPath must not be empty");
  }
  const provider = configuration.get<string>("provider", "none").trim();
  const model = configuration.get<string>("model", "").trim();
  const environment = buildReviewerEnvironment(process.env);
  let apiKey: string | undefined;

  if (provider === "openai") {
    if (model === "") {
      throw new Error("localCodeReviewer.model is required when the OpenAI provider is enabled");
    }
    if (!await approveAIEgress(context.workspaceState, folder, model)) {
      return;
    }
    apiKey = await getOrPromptForOpenAIKey(context.secrets);
    if (apiKey === undefined) {
      return;
    }
  } else if (provider !== "none") {
    throw new Error(`unsupported AI provider ${JSON.stringify(provider)}`);
  } else if (model !== "") {
    throw new Error("localCodeReviewer.model requires an enabled provider");
  }

  const execution = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: `Reviewing staged changes in ${folder.name}`,
      cancellable: true
    },
    async (_progress, token) => {
      const repositoryRoot = await discoverRepositoryRoot(folder.uri.fsPath, environment, token);
      const [canonicalRoot, canonicalFolder] = await Promise.all([
        realpath(repositoryRoot),
        realpath(folder.uri.fsPath)
      ]);
      if (canonicalRoot !== canonicalFolder) {
        throw new Error(`open the repository root as a workspace folder: ${repositoryRoot}`);
      }
      const args = ["review", "--staged", "--format", "json", "--repo", repositoryRoot];
      const reviewerEnv = { ...environment };
      if (apiKey !== undefined) {
        reviewerEnv[openAIKeyEnvironment] = apiKey;
        args.push("--ai-provider", provider, "--ai-model", model);
      }
      return {
        output: await runReviewer(binaryPath, args, repositoryRoot, reviewerEnv, token),
        repositoryRoot
      };
    }
  );
  const result = parseReviewResult(execution.output);
  publishDiagnostics(folder, execution.repositoryRoot, result, diagnostics, diagnosticUris);

  if (result.ai !== undefined && result.ai.failedBatches > 0) {
    void vscode.window.showWarningMessage(
      `Review completed with ${result.ai.failedBatches} failed AI batch(es); local findings were preserved.`
    );
  }
  const count = result.findings.length;
  void vscode.window.showInformationMessage(
    `Staged review completed: ${count} ${count === 1 ? "finding" : "findings"} in ${result.summary.filesReviewed} reviewed file(s).`
  );
  if (count > 0) {
    void vscode.commands.executeCommand("workbench.actions.view.problems");
  }
}

async function approveAIEgress(
  state: vscode.Memento,
  folder: vscode.WorkspaceFolder,
  model: string
): Promise<boolean> {
  const approvalKey = `aiEgressApproval:${folder.uri.toString()}`;
  const approval = `openai:${model}`;
  if (state.get<string>(approvalKey) === approval) {
    return true;
  }
  const selected = await vscode.window.showWarningMessage(
    `Allow staged code from ${folder.name} to be sent to OpenAI model ${model}? Environment files are excluded and detected secrets are redacted locally.`,
    { modal: true },
    "Allow AI Review"
  );
  if (selected !== "Allow AI Review") {
    return false;
  }
  await state.update(approvalKey, approval);
  return true;
}

async function selectWorkspaceFolder(): Promise<vscode.WorkspaceFolder | undefined> {
  const folders = (vscode.workspace.workspaceFolders ?? []).filter(folder => folder.uri.scheme === "file");
  if (folders.length === 0) {
    throw new Error("open a local workspace folder containing a Git repository first");
  }
  const activeDocument = vscode.window.activeTextEditor?.document.uri;
  if (activeDocument !== undefined) {
    const activeFolder = vscode.workspace.getWorkspaceFolder(activeDocument);
    if (activeFolder?.uri.scheme === "file") {
      return activeFolder;
    }
  }
  if (folders.length === 1) {
    return folders[0];
  }
  const selected = await vscode.window.showQuickPick(
    folders.map(folder => ({ label: folder.name, description: folder.uri.fsPath, folder })),
    { placeHolder: "Select the repository to review" }
  );
  return selected?.folder;
}

async function setOpenAIKey(secrets: vscode.SecretStorage): Promise<void> {
  const value = await vscode.window.showInputBox({
    title: "Local Code Reviewer: OpenAI API Key",
    prompt: "The key is stored in VS Code SecretStorage and is never written to workspace settings.",
    password: true,
    ignoreFocusOut: true,
    validateInput: input => input.trim() === "" ? "API key must not be empty" : undefined
  });
  if (value === undefined) {
    return;
  }
  await secrets.store(openAIKeySecret, value.trim());
  void vscode.window.showInformationMessage("OpenAI API key stored securely.");
}

async function clearOpenAIKey(secrets: vscode.SecretStorage): Promise<void> {
  await secrets.delete(openAIKeySecret);
  void vscode.window.showInformationMessage("Stored OpenAI API key cleared.");
}

async function getOrPromptForOpenAIKey(secrets: vscode.SecretStorage): Promise<string | undefined> {
  const stored = await secrets.get(openAIKeySecret);
  if (stored !== undefined && stored.trim() !== "") {
    return stored;
  }
  const selected = await vscode.window.showWarningMessage(
    "OpenAI review is enabled, but no API key is stored.",
    "Set API Key"
  );
  if (selected !== "Set API Key") {
    return undefined;
  }
  await setOpenAIKey(secrets);
  const value = await secrets.get(openAIKeySecret);
  return value === undefined || value.trim() === "" ? undefined : value;
}

function runReviewer(
  executable: string,
  args: string[],
  cwd: string,
  environment: NodeJS.ProcessEnv,
  token: vscode.CancellationToken
): Promise<string> {
  return runProcess(executable, args, cwd, environment, token, maxStdoutBytes, maxStderrBytes);
}

async function discoverRepositoryRoot(
  startDirectory: string,
  environment: NodeJS.ProcessEnv,
  token: vscode.CancellationToken
): Promise<string> {
  const output = await runProcess(
    "git",
    ["-C", startDirectory, "rev-parse", "--show-toplevel"],
    startDirectory,
    environment,
    token,
    64 * 1024,
    maxStderrBytes
  );
  const root = output.trim();
  if (root === "" || !path.isAbsolute(root)) {
    throw new Error("Git returned an invalid repository root");
  }
  return path.resolve(root);
}

function runProcess(
  executable: string,
  args: string[],
  cwd: string,
  environment: NodeJS.ProcessEnv,
  token: vscode.CancellationToken,
  stdoutLimit: number,
  stderrLimit: number
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
    let forceKillTimer: NodeJS.Timeout | undefined;
    let cancellation: vscode.Disposable | undefined;

    const finish = (action: () => void): void => {
      if (settled) {
        return;
      }
      settled = true;
      activeChildren.delete(child);
      cancellation?.dispose();
      if (forceKillTimer !== undefined) {
        clearTimeout(forceKillTimer);
      }
      action();
    };
    const requestStop = (error: Error): void => {
      if (stopError !== undefined || settled) {
        return;
      }
      stopError = error;
      child.kill("SIGTERM");
      forceKillTimer = setTimeout(() => child.kill("SIGKILL"), 5000);
      forceKillTimer.unref();
    };
    child.stdout.on("data", (chunk: Buffer) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > stdoutLimit) {
        requestStop(new Error(`${executable} stdout exceeded the safe output limit`));
        return;
      }
      stdout.push(chunk);
    });
    child.stderr.on("data", (chunk: Buffer) => {
      stderrBytes += chunk.length;
      if (stderrBytes > stderrLimit) {
        requestStop(new Error(`${executable} stderr exceeded the safe output limit`));
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
      const detail = cleanDisplayText(Buffer.concat(stderr).toString("utf8").trim());
      reject(new Error(detail === "" ? `${executable} exited with code ${code ?? "unknown"}` : detail));
    }));

    cancellation = token.onCancellationRequested(() => requestStop(new vscode.CancellationError()));
    if (token.isCancellationRequested) {
      requestStop(new vscode.CancellationError());
    }
  });
}

function publishDiagnostics(
  folder: vscode.WorkspaceFolder,
  repositoryRoot: string,
  result: ReviewResult,
  collection: vscode.DiagnosticCollection,
  diagnosticUris: Map<string, Map<string, vscode.Uri>>
): void {
  const folderKey = folder.uri.toString();
  for (const uri of diagnosticUris.get(folderKey)?.values() ?? []) {
    collection.delete(uri);
  }
  const grouped = new Map<string, { uri: vscode.Uri; diagnostics: vscode.Diagnostic[] }>();
  for (const finding of result.findings) {
    const uri = findingUri(repositoryRoot, finding.file);
    if (uri === undefined) {
      continue;
    }
    const key = uri.toString();
    const entry = grouped.get(key) ?? { uri, diagnostics: [] };
    entry.diagnostics.push(toDiagnostic(finding));
    grouped.set(key, entry);
  }
  const currentUris = new Map<string, vscode.Uri>();
  for (const entry of grouped.values()) {
    collection.set(entry.uri, entry.diagnostics);
    currentUris.set(entry.uri.toString(), entry.uri);
  }
  diagnosticUris.set(folderKey, currentUris);
}

function findingUri(repositoryRoot: string, file: string): vscode.Uri | undefined {
  const filePath = resolveFindingPath(repositoryRoot, file);
  return filePath === undefined ? undefined : vscode.Uri.file(filePath);
}

function toDiagnostic(finding: ReviewFinding): vscode.Diagnostic {
  const range = new vscode.Range(
    finding.startLine - 1,
    0,
    finding.endLine - 1,
    Number.MAX_SAFE_INTEGER
  );
  const suggestion = finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`;
  const diagnostic = new vscode.Diagnostic(
    range,
    cleanDisplayText(`${finding.title}\n\n${finding.message}${suggestion}`),
    diagnosticSeverity(finding.severity)
  );
  diagnostic.source = `Local Code Reviewer (${finding.source})`;
  diagnostic.code = finding.category;
  return diagnostic;
}

function diagnosticSeverity(severity: FindingSeverity): vscode.DiagnosticSeverity {
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

function executableError(executable: string, error: Error): Error {
  const code = (error as NodeJS.ErrnoException).code;
  if (code === "ENOENT") {
    return new Error(`reviewer executable ${JSON.stringify(executable)} was not found; configure localCodeReviewer.binaryPath`);
  }
  return error;
}

function cleanDisplayText(value: string): string {
  return Array.from(value, character => {
    if (character === "\n" || character === "\t") {
      return character;
    }
    return /[\p{Cc}\p{Cf}]/u.test(character) ? "\uFFFD" : character;
  }).join("");
}

function errorMessage(error: unknown): string {
  return cleanDisplayText(error instanceof Error ? error.message : String(error));
}
