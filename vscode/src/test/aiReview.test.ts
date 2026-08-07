import assert from "node:assert/strict";
import test from "node:test";

import { selectAIReview } from "../aiReview";
import { ReviewResult } from "../protocol";

const summary = {
  filesChanged: 2,
  filesReviewed: 2,
  filesSkipped: 0,
  hunksReviewed: 2,
  addedLines: 2,
  deletedLines: 0,
  findingCount: 2
};

test("selectAIReview disables diff UI for local-only results", () => {
  const result: ReviewResult = {
    schemaVersion: 2,
    reviewId: `sha256:${"a".repeat(64)}`,
    summary: { ...summary, findingCount: 0 },
    files: [],
    findings: []
  };
  assert.equal(selectAIReview(result, [{ path: "main.go", status: "M" }]), undefined);
});

test("selectAIReview uses provider-reviewed paths and AI findings only", () => {
  const result: ReviewResult = {
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
      agents: ["correctness"],
      batchCount: 1,
      successfulBatches: 1,
      failedBatches: 0
    }
  };
  const selection = selectAIReview(result, [
    { path: "main.go", status: "M" },
    { path: ".env", status: "M" }
  ]);
  assert.deepEqual(selection?.files, [{ path: "main.go", status: "M" }]);
  assert.equal(selection?.findings.length, 1);
  assert.equal(selection?.findings[0].source, "ai");
});

function finding(file: string, source: string) {
  return {
    file,
    startLine: 1,
    endLine: 1,
    severity: "medium" as const,
    category: "quality",
    title: "Review finding",
    message: "A review finding.",
    confidence: 0.8,
    source
  };
}
