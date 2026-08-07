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
exports.AICommentPresenter = void 0;
const vscode = __importStar(require("vscode"));
class AICommentPresenter {
    controller = vscode.comments.createCommentController("code-review.ai-comments", "Code Review AI Comments");
    threads = [];
    show(uri, findings) {
        this.clear();
        for (const finding of findings) {
            const line = Math.max(0, finding.startLine - 1);
            const body = new vscode.MarkdownString();
            body.appendMarkdown(`### ${finding.severity.toUpperCase()} · ${finding.category}\n\n`);
            body.appendMarkdown(`**${escapeMarkdown(finding.title)}**\n\n`);
            body.appendText(finding.message);
            if (finding.suggestion !== undefined && finding.suggestion.trim() !== "") {
                body.appendMarkdown("\n\n---\n\n**Suggested action**\n\n");
                body.appendText(finding.suggestion);
            }
            body.appendMarkdown(`\n\n_Agent: ${escapeMarkdown(finding.agentId ?? "AI review")}_`);
            const comment = {
                body,
                mode: vscode.CommentMode.Preview,
                author: { name: `Code Review · ${finding.agentId ?? "AI"}` },
                label: `${finding.severity} · ${finding.category}`
            };
            const thread = this.controller.createCommentThread(uri, new vscode.Range(line, 0, line, Number.MAX_SAFE_INTEGER), [comment]);
            thread.canReply = false;
            thread.collapsibleState = vscode.CommentThreadCollapsibleState.Expanded;
            this.threads.push(thread);
        }
    }
    clear() {
        for (const thread of this.threads) {
            thread.dispose();
        }
        this.threads = [];
    }
    dispose() {
        this.clear();
        this.controller.dispose();
    }
}
exports.AICommentPresenter = AICommentPresenter;
function escapeMarkdown(value) {
    return value.replace(/[\\`*_{}[\]()#+.!|>-]/g, "\\$&");
}
