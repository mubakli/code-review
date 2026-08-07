import assert from "node:assert/strict";
import test from "node:test";

import { parseReviewResult } from "../protocol";

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

test("parseReviewResult accepts the CLI schema", () => {
  const result = parseReviewResult(JSON.stringify(validResult));
  assert.equal(result.schemaVersion, 1);
  assert.equal(result.findings[0].file, "main.go");
});

test("parseReviewResult rejects unsupported schemas", () => {
  assert.throws(
    () => parseReviewResult(JSON.stringify({ ...validResult, schemaVersion: 2 })),
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
