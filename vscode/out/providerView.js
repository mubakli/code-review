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
exports.ProviderTreeProvider = exports.selectAgentsCommand = exports.manageAPIKeyCommand = exports.selectModelCommand = exports.configureProviderCommand = exports.providerViewID = void 0;
const vscode = __importStar(require("vscode"));
const agents_1 = require("./agents");
exports.providerViewID = "code-review.providerView";
exports.configureProviderCommand = "code-review.configureAIProvider";
exports.selectModelCommand = "code-review.selectAIModel";
exports.manageAPIKeyCommand = "code-review.manageAIKey";
exports.selectAgentsCommand = "code-review.selectAIAgents";
class ProviderTreeProvider {
    changedEmitter = new vscode.EventEmitter();
    state = {
        agents: [],
        autoReview: false,
        enabled: false,
        keyStored: false,
        model: ""
    };
    onDidChangeTreeData = this.changedEmitter.event;
    update(state) {
        this.state = state;
        this.changedEmitter.fire();
    }
    getTreeItem(element) {
        return element;
    }
    getChildren() {
        if (!this.state.enabled || this.state.provider === undefined) {
            return [item("AI review is off", "Choose OpenAI or DeepSeek", "circle-slash", exports.configureProviderCommand, "Choose AI Provider")];
        }
        const provider = this.state.provider;
        return [
            item(provider.label, "Selected provider", "sparkle", exports.configureProviderCommand, "Change AI Provider"),
            item(this.state.model || provider.defaultModel, "Model", "symbol-method", exports.selectModelCommand, "Change AI Model"),
            item((0, agents_1.reviewAgentSummary)(this.state.agents), "Review agents", "organization", exports.selectAgentsCommand, "Select AI Review Agents"),
            item(this.state.keyStored ? "API key stored" : "API key required", "SecretStorage", this.state.keyStored ? "key" : "warning", exports.manageAPIKeyCommand, "Manage API Key"),
            item(this.state.autoReview ? "Automatic review on" : "Automatic review off", "After staged changes", this.state.autoReview ? "sync" : "debug-pause", exports.configureProviderCommand, "Configure AI Review")
        ];
    }
}
exports.ProviderTreeProvider = ProviderTreeProvider;
function item(label, description, icon, command, title) {
    const value = new vscode.TreeItem(label, vscode.TreeItemCollapsibleState.None);
    value.description = description;
    value.iconPath = new vscode.ThemeIcon(icon);
    value.command = { command, title };
    return value;
}
