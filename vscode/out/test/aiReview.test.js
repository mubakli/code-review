"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const aiReview_1 = require("../aiReview");
const summary = {
    filesChanged: 2,
    filesReviewed: 2,
    filesSkipped: 0,
    hunksReviewed: 2,
    addedLines: 2,
    deletedLines: 0,
    findingCount: 2
};
(0, node_test_1.default)("selectAIReview disables diff UI for local-only results", () => {
    const result = {
        schemaVersion: 2,
        reviewId: `sha256:${"a".repeat(64)}`,
        summary: { ...summary, findingCount: 0 },
        files: [],
        findings: []
    };
    strict_1.default.equal((0, aiReview_1.selectAIReview)(result, [{ path: "main.go", status: "M" }]), undefined);
});
(0, node_test_1.default)("selectAIReview uses provider-reviewed paths and AI findings only", () => {
    const result = {
        schemaVersion: 2,
        reviewId: `sha256:${"a".repeat(64)}`,
        summary,
        files: [{ path: "main.go", status: "modified" }, { path: ".env", status: "modified" }],
        findings: [
            finding("main.go", "ai"),
            finding("main.go", "local-rule")
        ],
        ai: {
            provider: "openai",
            model: "review-model",
            reviewedFiles: ["main.go"],
            batchCount: 1,
            successfulBatches: 1,
            failedBatches: 0
        }
    };
    const selection = (0, aiReview_1.selectAIReview)(result, [
        { path: "main.go", status: "M" },
        { path: ".env", status: "M" }
    ]);
    strict_1.default.deepEqual(selection?.files, [{ path: "main.go", status: "M" }]);
    strict_1.default.equal(selection?.findings.length, 1);
    strict_1.default.equal(selection?.findings[0].source, "ai");
});
function finding(file, source) {
    return {
        file,
        startLine: 1,
        endLine: 1,
        severity: "medium",
        category: "quality",
        title: "Review finding",
        message: "A review finding.",
        confidence: 0.8,
        source
    };
}
