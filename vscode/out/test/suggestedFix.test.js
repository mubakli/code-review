"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const suggestedFix_1 = require("../suggestedFix");
(0, node_test_1.default)("applies a complete-line replacement without consuming its newline", () => {
    strict_1.default.equal((0, suggestedFix_1.applyProposedFix)("first\nold\nlast\n", { description: "Replace", startLine: 2, endLine: 2, replacement: "new" }), "first\nnew\nlast\n");
});
(0, node_test_1.default)("supports multiline deletion and CRLF preservation", () => {
    strict_1.default.equal((0, suggestedFix_1.applyProposedFix)("first\r\nold one\r\nold two\r\nlast\r\n", { description: "Replace", startLine: 2, endLine: 3, replacement: "new one\nnew two" }), "first\r\nnew one\r\nnew two\r\nlast\r\n");
    strict_1.default.equal((0, suggestedFix_1.applyProposedFix)("first\nremove\nlast", { description: "Delete", startLine: 2, endLine: 2, replacement: "" }), "first\nlast");
});
(0, node_test_1.default)("rejects a range outside the staged content", () => {
    strict_1.default.throws(() => (0, suggestedFix_1.applyProposedFix)("one\n", { description: "Replace", startLine: 3, endLine: 3, replacement: "three" }), /outside the staged file/);
});
