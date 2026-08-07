"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const stagedFiles_1 = require("../stagedFiles");
(0, node_test_1.default)("parseNameStatus parses ordinary staged files", () => {
    strict_1.default.deepEqual((0, stagedFiles_1.parseNameStatus)("M\0main.go\0A\0new file.ts\0"), [
        { path: "main.go", status: "M" },
        { path: "new file.ts", status: "A" }
    ]);
});
(0, node_test_1.default)("parseNameStatus parses rename scores and paths", () => {
    strict_1.default.deepEqual((0, stagedFiles_1.parseNameStatus)("R100\0old.go\0new.go\0"), [
        { path: "new.go", previousPath: "old.go", status: "R100" }
    ]);
});
(0, node_test_1.default)("parseNameStatus rejects incomplete output", () => {
    strict_1.default.throws(() => (0, stagedFiles_1.parseNameStatus)("R100\0old.go\0"), /incomplete staged rename/);
    strict_1.default.throws(() => (0, stagedFiles_1.parseNameStatus)("unknown\0file.go\0"), /unsupported staged status/);
});
