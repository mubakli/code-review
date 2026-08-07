"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const agents_1 = require("../agents");
(0, node_test_1.default)("agent configuration keeps supported unique selections", () => {
    strict_1.default.deepEqual((0, agents_1.configuredReviewAgentIDs)(["security"]), ["security"]);
    strict_1.default.deepEqual((0, agents_1.configuredReviewAgentIDs)(["security", "security", "unknown"]), ["security"]);
    strict_1.default.deepEqual((0, agents_1.configuredReviewAgentIDs)([]), ["correctness", "security"]);
});
(0, node_test_1.default)("agent summary follows the stable registry order", () => {
    strict_1.default.equal((0, agents_1.reviewAgentSummary)(["security", "correctness"]), "Correctness + Security");
    strict_1.default.equal((0, agents_1.reviewAgentSummary)(["security"]), "Security");
});
