import assert from "node:assert/strict";
import test from "node:test";

import { applyProposedFix } from "../suggestedFix";

test("applies a complete-line replacement without consuming its newline", () => {
  assert.equal(
    applyProposedFix("first\nold\nlast\n", { description: "Replace", startLine: 2, endLine: 2, replacement: "new" }),
    "first\nnew\nlast\n"
  );
});

test("supports multiline deletion and CRLF preservation", () => {
  assert.equal(
    applyProposedFix("first\r\nold one\r\nold two\r\nlast\r\n", { description: "Replace", startLine: 2, endLine: 3, replacement: "new one\nnew two" }),
    "first\r\nnew one\r\nnew two\r\nlast\r\n"
  );
  assert.equal(
    applyProposedFix("first\nremove\nlast", { description: "Delete", startLine: 2, endLine: 2, replacement: "" }),
    "first\nlast"
  );
});

test("rejects a range outside the staged content", () => {
  assert.throws(
    () => applyProposedFix("one\n", { description: "Replace", startLine: 3, endLine: 3, replacement: "three" }),
    /outside the staged file/
  );
});
