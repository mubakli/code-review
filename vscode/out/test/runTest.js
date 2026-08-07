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
const node_child_process_1 = require("node:child_process");
const node_fs_1 = require("node:fs");
const node_os_1 = require("node:os");
const path = __importStar(require("node:path"));
const test_electron_1 = require("@vscode/test-electron");
async function main() {
    const extensionDevelopmentPath = path.resolve(__dirname, "..", "..");
    const repositoryRoot = path.resolve(extensionDevelopmentPath, "..");
    const extensionTestsPath = path.resolve(__dirname, "suite", "index");
    const fixtureRoot = (0, node_fs_1.mkdtempSync)(path.join((0, node_os_1.tmpdir)(), "local-code-reviewer-vscode-"));
    const reviewerPath = path.join(fixtureRoot, process.platform === "win32" ? "reviewer.exe" : "reviewer");
    try {
        (0, node_child_process_1.execFileSync)("go", ["build", "-o", reviewerPath, "./cmd/reviewer"], {
            cwd: repositoryRoot,
            stdio: "inherit"
        });
        (0, node_child_process_1.execFileSync)("git", ["init", "--quiet", fixtureRoot], { stdio: "inherit" });
        (0, node_fs_1.writeFileSync)(path.join(fixtureRoot, "main.go"), "package sample\n\nconst apiKey = \"actual-secret-value-123\"\n", { mode: 0o600 });
        (0, node_child_process_1.execFileSync)("git", ["-C", fixtureRoot, "add", "--", "main.go"], { stdio: "inherit" });
        await (0, test_electron_1.runTests)({
            version: "1.96.4",
            extensionDevelopmentPath,
            extensionTestsPath,
            launchArgs: [
                fixtureRoot,
                "--disable-extensions",
                "--disable-workspace-trust"
            ]
        });
    }
    finally {
        (0, node_fs_1.rmSync)(fixtureRoot, { recursive: true, force: true });
    }
}
void main().catch(error => {
    console.error(error);
    process.exitCode = 1;
});
