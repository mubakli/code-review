import assert from "node:assert/strict";
import test from "node:test";

import { AutoReviewScheduler } from "../autoReview";

test("AutoReviewScheduler debounces and deduplicates staged snapshots", async () => {
  let snapshotCalls = 0;
  let reviewCalls = 0;
  const originalSnapshot = async () => {
    snapshotCalls++;
    return { reviewId: "sha256:a", dedupeKey: "sha256:a|local" };
  };
  const instrumented = new AutoReviewScheduler(10, originalSnapshot, async () => { reviewCalls++; });

  instrumented.notify("repo");
  instrumented.notify("repo");
  instrumented.notify("repo");
  await delay(40);
  assert.equal(snapshotCalls, 1);
  assert.equal(reviewCalls, 1);

  instrumented.notify("repo");
  await delay(30);
  assert.equal(reviewCalls, 1);
  instrumented.dispose();
});

test("AutoReviewScheduler cancels stale review and runs the latest snapshot", async () => {
  let reviewID = "sha256:a";
  const completed: string[] = [];
  const cancelled: string[] = [];
  const scheduler = new AutoReviewScheduler(
    5,
    async () => ({ reviewId: reviewID, dedupeKey: reviewID }),
    async (_repository, snapshot, signal) => {
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(resolve, snapshot.reviewId.endsWith("a") ? 100 : 5);
        signal.addEventListener("abort", () => {
          clearTimeout(timer);
          cancelled.push(snapshot.reviewId);
          reject(abortError());
        }, { once: true });
      });
      completed.push(snapshot.reviewId);
    }
  );

  scheduler.notify("repo");
  await delay(20);
  reviewID = "sha256:b";
  scheduler.notify("repo");
  await delay(40);

  assert.deepEqual(cancelled, ["sha256:a"]);
  assert.deepEqual(completed, ["sha256:b"]);
  scheduler.dispose();
});

function delay(milliseconds: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, milliseconds));
}

function abortError(): Error {
  const error = new Error("aborted");
  error.name = "AbortError";
  return error;
}
