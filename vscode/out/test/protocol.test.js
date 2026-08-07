"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const protocol_1 = require("../protocol");
const validResult = {
    schemaVersion: 1,
    summary: {
        filesChanged: 1,
        filesReviewed: 1,
        filesSkipped: 0,
        hunksReviewed: 1,
        addedLines: 1,
        deletedLines: 0,
        findingCount: 1
    },
    findings: [{
            file: "main.go",
            startLine: 3,
            endLine: 3,
            severity: "high",
            category: "security",
            title: "Potential credential",
            message: "A credential-like value was added.",
            suggestion: "Use secret storage.",
            confidence: 0.95,
            source: "local-rule"
        }]
};
(0, node_test_1.default)("parseReviewResult accepts the CLI schema", () => {
    const result = (0, protocol_1.parseReviewResult)(JSON.stringify(validResult));
    strict_1.default.equal(result.schemaVersion, 1);
    strict_1.default.equal(result.findings[0].file, "main.go");
});
(0, node_test_1.default)("parseReviewResult rejects unsupported schemas", () => {
    strict_1.default.throws(() => (0, protocol_1.parseReviewResult)(JSON.stringify({ ...validResult, schemaVersion: 2 })), /unsupported review schema version/);
});
(0, node_test_1.default)("parseReviewResult rejects invalid finding ranges", () => {
    const finding = { ...validResult.findings[0], startLine: 4, endLine: 3 };
    strict_1.default.throws(() => (0, protocol_1.parseReviewResult)(JSON.stringify({ ...validResult, findings: [finding] })), /endLine precedes startLine/);
});
(0, node_test_1.default)("parseReviewResult rejects inconsistent finding counts", () => {
    strict_1.default.throws(() => (0, protocol_1.parseReviewResult)(JSON.stringify({ ...validResult, findings: [] })), /findingCount does not match/);
});
(0, node_test_1.default)("parseReviewResult rejects unsupported finding sources", () => {
    const finding = { ...validResult.findings[0], source: "replacement-cli" };
    strict_1.default.throws(() => (0, protocol_1.parseReviewResult)(JSON.stringify({ ...validResult, findings: [finding] })), /source is unsupported/);
});
