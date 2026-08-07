import assert from "node:assert/strict";
import test from "node:test";

import { buildReviewerEnvironment } from "../environment";

test("buildReviewerEnvironment copies only process essentials", () => {
  const environment = buildReviewerEnvironment({
    PATH: "/usr/bin",
    HOME: "/tmp/home",
    HTTPS_PROXY: "http://proxy.invalid",
    REVIEWER_OPENAI_API_KEY: "secret",
    AWS_SECRET_ACCESS_KEY: "secret",
    SSH_AUTH_SOCK: "/tmp/agent.sock"
  });

  assert.deepEqual(environment, {
    PATH: "/usr/bin",
    HOME: "/tmp/home",
    HTTPS_PROXY: "http://proxy.invalid"
  });
});

test("buildReviewerEnvironment handles Windows key casing", () => {
  const environment = buildReviewerEnvironment({ Path: "C:\\Windows", SystemRoot: "C:\\Windows" });
  assert.deepEqual(environment, { Path: "C:\\Windows", SystemRoot: "C:\\Windows" });
});
