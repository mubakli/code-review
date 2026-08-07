"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const autoReview_1 = require("../autoReview");
(0, node_test_1.default)("AutoReviewScheduler debounces and deduplicates staged snapshots", async () => {
    let snapshotCalls = 0;
    let reviewCalls = 0;
    const originalSnapshot = async () => {
        snapshotCalls++;
        return { reviewId: "sha256:a", dedupeKey: "sha256:a|local" };
    };
    const instrumented = new autoReview_1.AutoReviewScheduler(10, originalSnapshot, async () => { reviewCalls++; });
    instrumented.notify("repo");
    instrumented.notify("repo");
    instrumented.notify("repo");
    await delay(40);
    strict_1.default.equal(snapshotCalls, 1);
    strict_1.default.equal(reviewCalls, 1);
    instrumented.notify("repo");
    await delay(30);
    strict_1.default.equal(reviewCalls, 1);
    instrumented.dispose();
});
(0, node_test_1.default)("AutoReviewScheduler cancels stale review and runs the latest snapshot", async () => {
    let reviewID = "sha256:a";
    const completed = [];
    const cancelled = [];
    const scheduler = new autoReview_1.AutoReviewScheduler(5, async () => ({ reviewId: reviewID, dedupeKey: reviewID }), async (_repository, snapshot, signal) => {
        await new Promise((resolve, reject) => {
            const timer = setTimeout(resolve, snapshot.reviewId.endsWith("a") ? 100 : 5);
            signal.addEventListener("abort", () => {
                clearTimeout(timer);
                cancelled.push(snapshot.reviewId);
                reject(abortError());
            }, { once: true });
        });
        completed.push(snapshot.reviewId);
    });
    scheduler.notify("repo");
    await delay(20);
    reviewID = "sha256:b";
    scheduler.notify("repo");
    await delay(40);
    strict_1.default.deepEqual(cancelled, ["sha256:a"]);
    strict_1.default.deepEqual(completed, ["sha256:b"]);
    scheduler.dispose();
});
function delay(milliseconds) {
    return new Promise(resolve => setTimeout(resolve, milliseconds));
}
function abortError() {
    const error = new Error("aborted");
    error.name = "AbortError";
    return error;
}
