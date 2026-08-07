"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.selectAIReview = selectAIReview;
function selectAIReview(result, stagedFiles) {
    if (result.ai === undefined) {
        return undefined;
    }
    const reviewedPaths = new Set(result.ai.reviewedFiles);
    const findings = result.findings.filter(finding => finding.source === "ai" && reviewedPaths.has(finding.file));
    const commentedPaths = new Set(findings.map(finding => finding.file));
    return {
        files: stagedFiles.filter(file => commentedPaths.has(file.path)),
        findings
    };
}
