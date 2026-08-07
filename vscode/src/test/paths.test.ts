import assert from "node:assert/strict";
import * as path from "node:path";
import test from "node:test";

import { resolveFindingPath } from "../paths";

test("resolveFindingPath resolves repository-relative files", () => {
  const root = path.resolve("repository");
  assert.equal(resolveFindingPath(root, "internal/main.go"), path.join(root, "internal", "main.go"));
});

test("resolveFindingPath rejects paths outside the repository", () => {
  const root = path.resolve("repository");
  assert.equal(resolveFindingPath(root, "../secret.txt"), undefined);
  assert.equal(resolveFindingPath(root, path.resolve("outside.txt")), undefined);
});
