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
  return {
    files: stagedFiles.filter(file => reviewedPaths.has(file.path)),
    findings: result.findings.filter(
      finding => finding.source === "ai" && reviewedPaths.has(finding.file)
    )
  };
}
