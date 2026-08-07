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
exports.StagedContentProvider = exports.ReviewTreeProvider = exports.indexContentScheme = exports.baseContentScheme = exports.openDiffCommand = exports.reviewViewID = void 0;
const path = __importStar(require("node:path"));
const vscode = __importStar(require("vscode"));
exports.reviewViewID = "code-review.reviewView";
exports.openDiffCommand = "code-review.openStagedDiff";
exports.baseContentScheme = "code-review-base";
exports.indexContentScheme = "code-review-index";
class ReviewTreeProvider {
    changedEmitter = new vscode.EventEmitter();
    files = [];
    onDidChangeTreeData = this.changedEmitter.event;
    setReview(files, findings) {
        const findingsByFile = new Map();
        for (const finding of findings) {
            const values = findingsByFile.get(finding.file) ?? [];
            values.push(finding);
            findingsByFile.set(finding.file, values);
        }
        this.files = files.map(file => new ReviewFileNode(file, findingsByFile.get(file.path) ?? []));
        this.changedEmitter.fire(undefined);
    }
    firstTarget() {
        const first = this.files.find(file => file.findings.length > 0) ?? this.files[0];
        return first === undefined
            ? undefined
            : { file: first.file, line: first.findings[0]?.startLine };
    }
    getTreeItem(element) {
        return element;
    }
    getChildren(element) {
        if (element === undefined) {
            return this.files;
        }
        return element instanceof ReviewFileNode ? element.children : [];
    }
}
exports.ReviewTreeProvider = ReviewTreeProvider;
class StagedContentProvider {
    content = new Map();
    sequence = 0;
    clear() {
        this.content.clear();
    }
    add(filePath, base, staged) {
        this.sequence++;
        const documentPath = `/${this.sequence}/${filePath.replaceAll("\\", "/")}`;
        const baseURI = vscode.Uri.from({ scheme: exports.baseContentScheme, path: documentPath });
        const stagedURI = vscode.Uri.from({ scheme: exports.indexContentScheme, path: documentPath });
        this.content.set(baseURI.toString(), base);
        this.content.set(stagedURI.toString(), staged);
        return { base: baseURI, staged: stagedURI };
    }
    provideTextDocumentContent(uri) {
        return this.content.get(uri.toString()) ?? "";
    }
}
exports.StagedContentProvider = StagedContentProvider;
class ReviewFileNode extends vscode.TreeItem {
    file;
    findings;
    children;
    constructor(file, findings) {
        super(path.basename(file.path), findings.length > 0 ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.None);
        this.file = file;
        this.findings = findings;
        const directory = path.dirname(file.path);
        const findingText = findings.length === 0 ? "" : `, ${findings.length} finding${findings.length === 1 ? "" : "s"}`;
        this.description = `${statusLabel(file.status)}${findingText}${directory === "." ? "" : ` - ${directory}`}`;
        this.contextValue = "reviewFile";
        this.iconPath = new vscode.ThemeIcon("diff");
        this.command = {
            command: exports.openDiffCommand,
            title: "Open Staged Diff",
            arguments: [{ file, line: findings[0]?.startLine }]
        };
        this.tooltip = `${file.path}\n${statusLabel(file.status)} staged change`;
        this.children = findings.map(finding => new ReviewFindingNode(file, finding));
    }
}
class ReviewFindingNode extends vscode.TreeItem {
    constructor(file, finding) {
        super(finding.title, vscode.TreeItemCollapsibleState.None);
        this.description = `line ${finding.startLine} - ${finding.severity}`;
        this.contextValue = "reviewFinding";
        this.iconPath = new vscode.ThemeIcon(severityIcon(finding.severity));
        this.command = {
            command: exports.openDiffCommand,
            title: "Open Finding in Staged Diff",
            arguments: [{ file, line: finding.startLine }]
        };
        this.tooltip = `${finding.message}${finding.suggestion === undefined ? "" : `\n\nSuggestion: ${finding.suggestion}`}`;
    }
}
function statusLabel(status) {
    switch (status[0]) {
        case "A": return "Added";
        case "C": return "Copied";
        case "D": return "Deleted";
        case "M": return "Modified";
        case "R": return "Renamed";
        case "T": return "Type changed";
        default: return "Changed";
    }
}
function severityIcon(severity) {
    switch (severity) {
        case "critical":
        case "high":
            return "error";
        case "medium":
            return "warning";
        case "low":
            return "info";
        case "info":
            return "lightbulb";
    }
}
