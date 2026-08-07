import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import * as path from "node:path";

import { runTests } from "@vscode/test-electron";

async function main(): Promise<void> {
  const extensionDevelopmentPath = path.resolve(__dirname, "..", "..");
  const repositoryRoot = path.resolve(extensionDevelopmentPath, "..");
  const extensionTestsPath = path.resolve(__dirname, "suite", "index");
  const fixtureRoot = mkdtempSync(path.join(tmpdir(), "local-code-reviewer-vscode-"));
  const reviewerPath = path.join(fixtureRoot, process.platform === "win32" ? "reviewer.exe" : "reviewer");

  try {
    execFileSync("go", ["build", "-o", reviewerPath, "./cmd/reviewer"], {
      cwd: repositoryRoot,
      stdio: "inherit"
    });
    execFileSync("git", ["init", "--quiet", fixtureRoot], { stdio: "inherit" });
    writeFileSync(
      path.join(fixtureRoot, "main.go"),
      "package sample\n\nconst apiKey = \"actual-secret-value-123\"\n",
      { mode: 0o600 }
    );
    execFileSync("git", ["-C", fixtureRoot, "add", "--", "main.go"], { stdio: "inherit" });

    await runTests({
      version: "1.96.4",
      extensionDevelopmentPath,
      extensionTestsPath,
      extensionTestsEnv: {
        REVIEWER_TEST_BINARY: reviewerPath
      },
      launchArgs: [
        fixtureRoot,
        "--disable-extensions",
        "--disable-workspace-trust"
      ]
    });
  } finally {
    rmSync(fixtureRoot, { recursive: true, force: true });
  }
}

void main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
