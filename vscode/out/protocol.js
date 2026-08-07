"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.reviewSchemaVersion = void 0;
exports.parseReviewResult = parseReviewResult;
exports.parseSnapshotResult = parseSnapshotResult;
exports.reviewSchemaVersion = 2;
const severities = new Set(["critical", "high", "medium", "low", "info"]);
const categories = new Set(["security", "correctness", "performance", "database", "maintainability", "quality"]);
const sources = new Set(["local-rule", "static-analysis", "sql-analyzer", "ai"]);
function parseReviewResult(output) {
    let value;
    try {
        value = JSON.parse(output);
    }
    catch (error) {
        throw new Error(`reviewer returned invalid JSON: ${errorMessage(error)}`);
    }
    const root = record(value, "review result");
    const schemaVersion = integer(root.schemaVersion, "schemaVersion");
    if (schemaVersion !== exports.reviewSchemaVersion) {
        throw new Error(`unsupported review schema version ${schemaVersion}`);
    }
    const summaryValue = record(root.summary, "summary");
    const summary = {
        filesChanged: nonNegativeInteger(summaryValue.filesChanged, "summary.filesChanged"),
        filesReviewed: nonNegativeInteger(summaryValue.filesReviewed, "summary.filesReviewed"),
        filesSkipped: nonNegativeInteger(summaryValue.filesSkipped, "summary.filesSkipped"),
        hunksReviewed: nonNegativeInteger(summaryValue.hunksReviewed, "summary.hunksReviewed"),
        addedLines: nonNegativeInteger(summaryValue.addedLines, "summary.addedLines"),
        deletedLines: nonNegativeInteger(summaryValue.deletedLines, "summary.deletedLines"),
        findingCount: nonNegativeInteger(summaryValue.findingCount, "summary.findingCount")
    };
    if (!Array.isArray(root.findings)) {
        throw new Error("findings must be an array");
    }
    const findings = root.findings.map(parseFinding);
    if (summary.findingCount !== findings.length) {
        throw new Error("summary.findingCount does not match findings length");
    }
    if (!Array.isArray(root.files)) {
        throw new Error("files must be an array");
    }
    return {
        schemaVersion,
        reviewId: reviewID(root.reviewId),
        summary,
        files: root.files.map(parseReviewFile),
        findings,
        ai: root.ai === undefined ? undefined : parseAI(root.ai)
    };
}
function parseSnapshotResult(output) {
    let value;
    try {
        value = JSON.parse(output);
    }
    catch (error) {
        throw new Error(`reviewer returned invalid snapshot JSON: ${errorMessage(error)}`);
    }
    const snapshot = record(value, "snapshot result");
    const schemaVersion = integer(snapshot.schemaVersion, "schemaVersion");
    if (schemaVersion !== exports.reviewSchemaVersion) {
        throw new Error(`unsupported review schema version ${schemaVersion}`);
    }
    return {
        schemaVersion,
        reviewId: reviewID(snapshot.reviewId),
        filesChanged: nonNegativeInteger(snapshot.filesChanged, "filesChanged")
    };
}
function parseReviewFile(value, index) {
    const prefix = `files[${index}]`;
    const file = record(value, prefix);
    const status = text(file.status, `${prefix}.status`);
    if (!new Set(["modified", "added", "deleted", "renamed", "copied"]).has(status)) {
        throw new Error(`${prefix}.status is unsupported`);
    }
    return {
        path: singleLineText(file.path, `${prefix}.path`),
        previousPath: file.previousPath === undefined ? undefined : singleLineText(file.previousPath, `${prefix}.previousPath`),
        status,
        binary: file.binary === undefined ? undefined : boolean(file.binary, `${prefix}.binary`)
    };
}
function parseFinding(value, index) {
    const prefix = `findings[${index}]`;
    const finding = record(value, prefix);
    const severity = text(finding.severity, `${prefix}.severity`);
    if (!severities.has(severity)) {
        throw new Error(`${prefix}.severity is unsupported`);
    }
    const startLine = positiveInteger(finding.startLine, `${prefix}.startLine`);
    const endLine = positiveInteger(finding.endLine, `${prefix}.endLine`);
    if (endLine < startLine) {
        throw new Error(`${prefix}.endLine precedes startLine`);
    }
    const confidence = finiteNumber(finding.confidence, `${prefix}.confidence`);
    if (confidence < 0 || confidence > 1) {
        throw new Error(`${prefix}.confidence must be between 0 and 1`);
    }
    const category = text(finding.category, `${prefix}.category`);
    if (!categories.has(category)) {
        throw new Error(`${prefix}.category is unsupported`);
    }
    const source = text(finding.source, `${prefix}.source`);
    if (!sources.has(source)) {
        throw new Error(`${prefix}.source is unsupported`);
    }
    return {
        file: singleLineText(finding.file, `${prefix}.file`),
        startLine,
        endLine,
        severity,
        category,
        title: text(finding.title, `${prefix}.title`),
        message: text(finding.message, `${prefix}.message`),
        suggestion: optionalText(finding.suggestion, `${prefix}.suggestion`),
        confidence,
        source,
        agentId: finding.agentId === undefined ? undefined : singleLineText(finding.agentId, `${prefix}.agentId`)
    };
}
function parseAI(value) {
    const ai = record(value, "ai");
    return {
        provider: text(ai.provider, "ai.provider"),
        model: text(ai.model, "ai.model"),
        reviewedFiles: ai.reviewedFiles === undefined ? [] : textArray(ai.reviewedFiles, "ai.reviewedFiles"),
        agents: ai.agents === undefined ? [] : textArray(ai.agents, "ai.agents"),
        batchCount: nonNegativeInteger(ai.batchCount, "ai.batchCount"),
        successfulBatches: nonNegativeInteger(ai.successfulBatches, "ai.successfulBatches"),
        failedBatches: nonNegativeInteger(ai.failedBatches, "ai.failedBatches")
    };
}
function textArray(value, name) {
    if (!Array.isArray(value)) {
        throw new Error(`${name} must be an array`);
    }
    return value.map((entry, index) => singleLineText(entry, `${name}[${index}]`));
}
function record(value, name) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
        throw new Error(`${name} must be an object`);
    }
    return value;
}
function text(value, name) {
    if (typeof value !== "string" || value.trim() === "") {
        throw new Error(`${name} must be a non-empty string`);
    }
    return value;
}
function optionalText(value, name) {
    if (value === undefined) {
        return undefined;
    }
    return text(value, name);
}
function singleLineText(value, name) {
    const result = text(value, name);
    if (/\p{C}/u.test(result)) {
        throw new Error(`${name} contains control characters`);
    }
    return result;
}
function finiteNumber(value, name) {
    if (typeof value !== "number" || !Number.isFinite(value)) {
        throw new Error(`${name} must be a finite number`);
    }
    return value;
}
function boolean(value, name) {
    if (typeof value !== "boolean") {
        throw new Error(`${name} must be a boolean`);
    }
    return value;
}
function reviewID(value) {
    const result = singleLineText(value, "reviewId");
    if (!/^sha256:[0-9a-f]{64}$/.test(result)) {
        throw new Error("reviewId must be a SHA-256 identifier");
    }
    return result;
}
function integer(value, name) {
    const result = finiteNumber(value, name);
    if (!Number.isInteger(result)) {
        throw new Error(`${name} must be an integer`);
    }
    return result;
}
function positiveInteger(value, name) {
    const result = integer(value, name);
    if (result < 1) {
        throw new Error(`${name} must be positive`);
    }
    return result;
}
function nonNegativeInteger(value, name) {
    const result = integer(value, name);
    if (result < 0) {
        throw new Error(`${name} must not be negative`);
    }
    return result;
}
function errorMessage(error) {
    return error instanceof Error ? error.message : String(error);
}
