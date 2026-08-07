import assert from "node:assert/strict";
import test from "node:test";

import { parseReviewResult, parseSnapshotResult } from "../protocol";

const validResult = {
  schemaVersion: 3,
  reviewId: `sha256:${"a".repeat(64)}`,
  summary: {
    filesChanged: 1,
    filesReviewed: 1,
    filesSkipped: 0,
    hunksReviewed: 1,
    addedLines: 1,
    deletedLines: 0,
    findingCount: 1
  },
  files: [{ path: "main.go", status: "modified" }],
  findings: [{
    ruleId: "secrets/hardcoded-secret",
    findingId: `sha256:${"c".repeat(64)}`,
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

test("parseReviewResult accepts the CLI schema", () => {
  const result = parseReviewResult(JSON.stringify(validResult));
  assert.equal(result.schemaVersion, 3);
  assert.equal(result.findings[0].file, "main.go");
});

test("parseReviewResult rejects unsupported schemas", () => {
  assert.throws(
    () => parseReviewResult(JSON.stringify({ ...validResult, schemaVersion: 1 })),
    /unsupported review schema version/
  );
});

test("parseReviewResult rejects invalid finding ranges", () => {
  const finding = { ...validResult.findings[0], startLine: 4, endLine: 3 };
  assert.throws(
    () => parseReviewResult(JSON.stringify({ ...validResult, findings: [finding] })),
    /endLine precedes startLine/
  );
});

test("parseReviewResult rejects inconsistent finding counts", () => {
  assert.throws(
    () => parseReviewResult(JSON.stringify({ ...validResult, findings: [] })),
    /findingCount does not match/
  );
});

test("parseReviewResult rejects unsupported finding sources", () => {
  const finding = { ...validResult.findings[0], source: "replacement-cli" };
  assert.throws(
    () => parseReviewResult(JSON.stringify({ ...validResult, findings: [finding] })),
    /source is unsupported/
  );
});

test("parseReviewResult accepts AI-reviewed file paths", () => {
  const result = parseReviewResult(JSON.stringify({
    ...validResult,
    ai: {
      provider: "openai",
      model: "review-model",
      reviewedFiles: ["main.go"],
      agents: ["correctness"],
      batchCount: 1,
      successfulBatches: 1,
      failedBatches: 0
    }
  }));
  assert.deepEqual(result.ai?.reviewedFiles, ["main.go"]);
  assert.deepEqual(result.ai?.agents, ["correctness"]);
});

test("parseReviewResult validates structured proposed fixes", () => {
  const proposedFix = {
    description: "Use the returned error.",
    startLine: 3,
    endLine: 3,
    replacement: "if err != nil { return err }"
  };
  const result = parseReviewResult(JSON.stringify({
    ...validResult,
    findings: [{ ...validResult.findings[0], proposedFix }]
  }));
  assert.deepEqual(result.findings[0].proposedFix, proposedFix);

  assert.throws(
    () => parseReviewResult(JSON.stringify({
      ...validResult,
      findings: [{ ...validResult.findings[0], proposedFix: { ...proposedFix, startLine: 2 } }]
    })),
    /range must match/
  );
});

test("parseReviewResult requires stable rule and finding IDs", () => {
  assert.throws(
    () => parseReviewResult(JSON.stringify({
      ...validResult,
      findings: [{ ...validResult.findings[0], ruleId: "Security Rule" }]
    })),
    /safe lowercase namespace/
  );
  assert.throws(
    () => parseReviewResult(JSON.stringify({
      ...validResult,
      findings: [{ ...validResult.findings[0], findingId: "unstable" }]
    })),
    /SHA-256 identifier/
  );
});

test("parseSnapshotResult validates deterministic review IDs", () => {
  const snapshot = parseSnapshotResult(JSON.stringify({
    schemaVersion: 3,
    reviewId: `sha256:${"b".repeat(64)}`,
    filesChanged: 3
  }));
  assert.equal(snapshot.filesChanged, 3);
  assert.throws(
    () => parseSnapshotResult(JSON.stringify({ ...snapshot, reviewId: "not-a-hash" })),
    /SHA-256 identifier/
  );
});
