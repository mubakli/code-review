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
exports.SuggestedFixController = exports.applySuggestedFixCommand = exports.previewSuggestedFixCommand = exports.fixContentScheme = void 0;
const promises_1 = require("node:fs/promises");
const path = __importStar(require("node:path"));
const vscode = __importStar(require("vscode"));
const paths_1 = require("./paths");
const suggestedFix_1 = require("./suggestedFix");
exports.fixContentScheme = "code-review-suggested";
exports.previewSuggestedFixCommand = "code-review.previewSuggestedFix";
exports.applySuggestedFixCommand = "code-review.applySuggestedFix";
class SuggestedFixController {
    currentReviewID;
    stagedContent;
    changedEmitter = new vscode.EventEmitter();
    diagnosticTargets = new WeakMap();
    previews = new Map();
    targets = new Map();
    sequence = 0;
    onDidChange = this.changedEmitter.event;
    constructor(currentReviewID, stagedContent) {
        this.currentReviewID = currentReviewID;
        this.stagedContent = stagedContent;
    }
    setReview(session, findings) {
        this.clearRepository(session.repositoryRoot);
        for (const finding of findings) {
            if (finding.proposedFix !== undefined) {
                this.targets.set(finding.findingId, { finding, session });
            }
        }
    }
    clearRepository(repositoryRoot) {
        for (const [id, target] of this.targets) {
            if (target.session.repositoryRoot === repositoryRoot) {
                this.targets.delete(id);
            }
        }
        for (const [uri, preview] of this.previews) {
            if (preview.session.repositoryRoot === repositoryRoot) {
                this.previews.delete(uri);
            }
        }
    }
    registerDiagnostic(diagnostic, finding) {
        if (finding.proposedFix !== undefined && this.targets.has(finding.findingId)) {
            this.diagnosticTargets.set(diagnostic, finding.findingId);
        }
    }
    provideTextDocumentContent(uri) {
        const preview = this.previews.get(uri.toString());
        if (preview === undefined) {
            throw new Error("Suggested fix preview is no longer available");
        }
        return uri.path.endsWith("/staged") ? preview.staged : preview.suggested;
    }
    provideCodeActions(_document, _range, context) {
        const actions = [];
        for (const diagnostic of context.diagnostics) {
            const findingID = this.diagnosticTargets.get(diagnostic);
            if (findingID === undefined || !this.targets.has(findingID)) {
                continue;
            }
            const action = new vscode.CodeAction("Preview Suggested Fix", vscode.CodeActionKind.QuickFix);
            action.command = { command: exports.previewSuggestedFixCommand, title: action.title, arguments: [findingID] };
            action.diagnostics = [diagnostic];
            actions.push(action);
        }
        return actions;
    }
    async preview(value) {
        const findingID = findingIDFrom(value);
        const target = findingID === undefined ? undefined : this.targets.get(findingID);
        if (target === undefined || target.finding.proposedFix === undefined) {
            void vscode.window.showWarningMessage("This suggested fix is no longer available. Run the review again.");
            return;
        }
        try {
            const preview = await vscode.window.withProgress({ location: vscode.ProgressLocation.Window, title: "Preparing suggested fix preview", cancellable: true }, async (_progress, token) => {
                await this.requireCurrentReview(target.session, token);
                const staged = await this.stagedContent(target.session, target.finding.file, token);
                await this.requireCurrentReview(target.session, token);
                const suggested = (0, suggestedFix_1.applyProposedFix)(staged, target.finding.proposedFix);
                const id = `${target.finding.findingId.slice(7, 23)}-${++this.sequence}`;
                const stagedURI = vscode.Uri.from({ scheme: exports.fixContentScheme, path: `/${id}/staged` });
                const suggestedURI = vscode.Uri.from({ scheme: exports.fixContentScheme, path: `/${id}/suggested` });
                const value = { ...target, staged, suggested, stagedURI, suggestedURI };
                this.previews.set(stagedURI.toString(), value);
                this.previews.set(suggestedURI.toString(), value);
                return value;
            });
            await vscode.commands.executeCommand("vscode.diff", preview.stagedURI, preview.suggestedURI, `${target.finding.file} (Staged ↔ Suggested Fix)`, { preview: true });
        }
        catch (error) {
            if (!(error instanceof vscode.CancellationError)) {
                void vscode.window.showWarningMessage(`Could not preview suggested fix: ${displayError(error)}`);
            }
        }
    }
    async apply(value) {
        const uri = value instanceof vscode.Uri ? value : undefined;
        const preview = uri === undefined ? undefined : this.previews.get(uri.toString());
        if (preview === undefined) {
            void vscode.window.showWarningMessage("Open a current suggested fix preview before applying it.");
            return;
        }
        const approval = await vscode.window.showWarningMessage(`Apply this suggested fix to ${preview.finding.file}? The edit will remain unsaved and will not be staged.`, { modal: true }, "Apply to Working Tree");
        if (approval !== "Apply to Working Tree") {
            return;
        }
        try {
            await vscode.window.withProgress({ location: vscode.ProgressLocation.Window, title: "Validating and applying suggested fix", cancellable: false }, async (_progress, token) => {
                const targetURI = await safeTargetURI(preview.session.repositoryRoot, preview.finding.file);
                await this.requireCurrentReview(preview.session, token);
                const staged = await this.stagedContent(preview.session, preview.finding.file, token);
                await this.requireCurrentReview(preview.session, token);
                if (staged !== preview.staged) {
                    throw new Error("the staged file changed after this preview was created");
                }
                const document = await vscode.workspace.openTextDocument(targetURI);
                if (document.isDirty || document.getText() !== staged) {
                    throw new Error("the working-tree file has unstaged or unsaved changes");
                }
                const version = document.version;
                const edit = new vscode.WorkspaceEdit();
                edit.replace(targetURI, new vscode.Range(document.positionAt(0), document.positionAt(staged.length)), preview.suggested);
                if (document.version !== version || document.isDirty || document.getText() !== staged) {
                    throw new Error("the working-tree file changed while the fix was being validated");
                }
                if (!await vscode.workspace.applyEdit(edit)) {
                    throw new Error("VS Code rejected the workspace edit");
                }
                await vscode.window.showTextDocument(document);
            });
            void vscode.window.showInformationMessage("Suggested fix applied as an unsaved edit. Review it, then save and stage it manually.");
        }
        catch (error) {
            void vscode.window.showWarningMessage(`Suggested fix was not applied: ${displayError(error)}`);
        }
    }
    dispose() {
        this.targets.clear();
        this.previews.clear();
        this.changedEmitter.dispose();
    }
    async requireCurrentReview(session, token) {
        if (await this.currentReviewID(session, token) !== session.reviewId) {
            throw new Error("the staged snapshot changed; run the review again");
        }
    }
}
exports.SuggestedFixController = SuggestedFixController;
function findingIDFrom(value) {
    if (typeof value === "string") {
        return value;
    }
    if (typeof value === "object" && value !== null && "finding" in value) {
        const finding = value.finding;
        if (typeof finding === "object" && finding !== null && "findingId" in finding) {
            const id = finding.findingId;
            return typeof id === "string" ? id : undefined;
        }
    }
    return undefined;
}
async function safeTargetURI(repositoryRoot, file) {
    if (!vscode.workspace.isTrusted) {
        throw new Error("the workspace is not trusted");
    }
    const resolved = (0, paths_1.resolveFindingPath)(repositoryRoot, file);
    if (resolved === undefined) {
        throw new Error("the finding path is outside the repository");
    }
    const uri = vscode.Uri.file(resolved);
    const folder = vscode.workspace.getWorkspaceFolder(uri);
    if (folder === undefined || folder.uri.scheme !== "file") {
        throw new Error("the finding path is outside the open workspace");
    }
    const [rootPath, folderPath, targetPath, targetInfo] = await Promise.all([
        (0, promises_1.realpath)(repositoryRoot),
        (0, promises_1.realpath)(folder.uri.fsPath),
        (0, promises_1.realpath)(resolved),
        (0, promises_1.lstat)(resolved)
    ]);
    if (targetInfo.isSymbolicLink() || !isInside(rootPath, targetPath) || !isInside(folderPath, targetPath)) {
        throw new Error("the finding path resolves outside a writable repository file");
    }
    return uri;
}
function isInside(parent, candidate) {
    const relative = path.relative(parent, candidate);
    return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== ".." && !path.isAbsolute(relative));
}
function displayError(error) {
    return error instanceof Error ? error.message : String(error);
}
