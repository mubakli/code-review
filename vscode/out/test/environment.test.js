"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const environment_1 = require("../environment");
(0, node_test_1.default)("buildReviewerEnvironment copies only process essentials", () => {
    const environment = (0, environment_1.buildReviewerEnvironment)({
        PATH: "/usr/bin",
        HOME: "/tmp/home",
        HTTPS_PROXY: "http://proxy.invalid",
        REVIEWER_OPENAI_API_KEY: "secret",
        AWS_SECRET_ACCESS_KEY: "secret",
        SSH_AUTH_SOCK: "/tmp/agent.sock"
    });
    strict_1.default.deepEqual(environment, {
        PATH: "/usr/bin",
        HOME: "/tmp/home",
        HTTPS_PROXY: "http://proxy.invalid"
    });
});
(0, node_test_1.default)("buildReviewerEnvironment handles Windows key casing", () => {
    const environment = (0, environment_1.buildReviewerEnvironment)({ Path: "C:\\Windows", SystemRoot: "C:\\Windows" });
    strict_1.default.deepEqual(environment, { Path: "C:\\Windows", SystemRoot: "C:\\Windows" });
});
