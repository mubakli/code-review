"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.reviewSchemaVersion = void 0;
exports.parseReviewResult = parseReviewResult;
exports.reviewSchemaVersion = 1;
const severities = new Set(["critical", "high", "medium", "low", "info"]);
const categories = new Set(["security", "performance", "database", "maintainability", "quality"]);
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
    return {
        schemaVersion,
        summary,
        findings,
        ai: root.ai === undefined ? undefined : parseAI(root.ai)
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
        source
    };
}
function parseAI(value) {
    const ai = record(value, "ai");
    return {
        provider: text(ai.provider, "ai.provider"),
        model: text(ai.model, "ai.model"),
        batchCount: nonNegativeInteger(ai.batchCount, "ai.batchCount"),
        successfulBatches: nonNegativeInteger(ai.successfulBatches, "ai.successfulBatches"),
        failedBatches: nonNegativeInteger(ai.failedBatches, "ai.failedBatches")
    };
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
