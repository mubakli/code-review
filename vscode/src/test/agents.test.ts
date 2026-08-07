import assert from "node:assert/strict";
import test from "node:test";

import { configuredReviewAgentIDs, reviewAgentSummary } from "../agents";

test("agent configuration keeps supported unique selections", () => {
  assert.deepEqual(configuredReviewAgentIDs(["security"]), ["security"]);
  assert.deepEqual(configuredReviewAgentIDs(["security", "security", "unknown"]), ["security"]);
  assert.deepEqual(configuredReviewAgentIDs([]), ["correctness", "security"]);
});

test("agent summary follows the stable registry order", () => {
  assert.equal(reviewAgentSummary(["security", "correctness"]), "Correctness + Security");
  assert.equal(reviewAgentSummary(["security"]), "Security");
});
