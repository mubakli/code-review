import { ReviewFinding, ReviewResult } from "./protocol";
import { StagedFile } from "./stagedFiles";

export interface AIReviewSelection {
  files: StagedFile[];
  findings: ReviewFinding[];
}

export function selectAIReview(
  result: ReviewResult,
  stagedFiles: StagedFile[]
): AIReviewSelection | undefined {
  if (result.ai === undefined) {
    return undefined;
  }
  const reviewedPaths = new Set(result.ai.reviewedFiles);
  const findings = result.findings.filter(
    finding => finding.source === "ai" && reviewedPaths.has(finding.file)
  );
  const commentedPaths = new Set(findings.map(finding => finding.file));
  return {
    files: stagedFiles.filter(file => commentedPaths.has(file.path)),
    findings
  };
}
