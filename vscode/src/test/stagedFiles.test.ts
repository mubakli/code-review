import assert from "node:assert/strict";
import test from "node:test";

import { parseNameStatus } from "../stagedFiles";

test("parseNameStatus parses ordinary staged files", () => {
  assert.deepEqual(parseNameStatus("M\0main.go\0A\0new file.ts\0"), [
    { path: "main.go", status: "M" },
    { path: "new file.ts", status: "A" }
  ]);
});

test("parseNameStatus parses rename scores and paths", () => {
  assert.deepEqual(parseNameStatus("R100\0old.go\0new.go\0"), [
    { path: "new.go", previousPath: "old.go", status: "R100" }
  ]);
});

test("parseNameStatus rejects incomplete output", () => {
  assert.throws(() => parseNameStatus("R100\0old.go\0"), /incomplete staged rename/);
  assert.throws(() => parseNameStatus("unknown\0file.go\0"), /unsupported staged status/);
});
