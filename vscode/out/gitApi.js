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
exports.watchGitRepositories = watchGitRepositories;
const vscode = __importStar(require("vscode"));
async function watchGitRepositories(onStagedStateHint) {
    const disposables = [];
    const repositorySubscriptions = new Map();
    const extension = vscode.extensions.getExtension("vscode.git");
    if (extension === undefined) {
        return new vscode.Disposable(() => { });
    }
    const git = await extension.activate();
    const api = git.getAPI(1);
    const attach = (repository) => {
        const key = repository.rootUri.toString();
        if (repositorySubscriptions.has(key)) {
            return;
        }
        const subscription = repository.state.onDidChange(() => onStagedStateHint(repository.rootUri));
        repositorySubscriptions.set(key, subscription);
        onStagedStateHint(repository.rootUri);
    };
    const detach = (repository) => {
        const key = repository.rootUri.toString();
        repositorySubscriptions.get(key)?.dispose();
        repositorySubscriptions.delete(key);
    };
    for (const repository of api.repositories) {
        attach(repository);
    }
    disposables.push(api.onDidOpenRepository(attach), api.onDidCloseRepository(detach));
    return new vscode.Disposable(() => {
        for (const disposable of disposables) {
            disposable.dispose();
        }
        for (const subscription of repositorySubscriptions.values()) {
            subscription.dispose();
        }
        repositorySubscriptions.clear();
    });
}
