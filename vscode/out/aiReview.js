"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.selectAIReview = selectAIReview;
function selectAIReview(result, stagedFiles) {
    if (result.ai === undefined) {
        return undefined;
    }
    const reviewedPaths = new Set(result.ai.reviewedFiles);
    return {
        files: stagedFiles.filter(file => reviewedPaths.has(file.path)),
        findings: result.findings.filter(finding => finding.source === "ai" && reviewedPaths.has(finding.file))
    };
}
