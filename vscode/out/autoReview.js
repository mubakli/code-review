"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.AutoReviewScheduler = void 0;
class AutoReviewScheduler {
    debounceMs;
    snapshot;
    review;
    onStatus;
    onError;
    states = new Map();
    disposed = false;
    constructor(debounceMs, snapshot, review, onStatus = () => { }, onError = () => { }) {
        this.debounceMs = debounceMs;
        this.snapshot = snapshot;
        this.review = review;
        this.onStatus = onStatus;
        this.onError = onError;
    }
    setDebounce(milliseconds) {
        this.debounceMs = milliseconds;
    }
    notify(repository, debounceMs = this.debounceMs) {
        if (this.disposed) {
            return;
        }
        const state = this.state(repository);
        if (state.timer !== undefined) {
            clearTimeout(state.timer);
        }
        this.onStatus(repository, "scheduled");
        state.timer = setTimeout(() => {
            state.timer = undefined;
            void this.evaluate(repository, false);
        }, debounceMs);
    }
    async runNow(repository) {
        if (this.disposed) {
            return;
        }
        const state = this.state(repository);
        if (state.timer !== undefined) {
            clearTimeout(state.timer);
            state.timer = undefined;
        }
        await this.evaluate(repository, true);
    }
    dispose() {
        this.disposed = true;
        for (const state of this.states.values()) {
            if (state.timer !== undefined) {
                clearTimeout(state.timer);
            }
            state.active?.controller.abort();
        }
        this.states.clear();
    }
    async evaluate(repository, force) {
        const state = this.state(repository);
        const snapshotController = new AbortController();
        let snapshot;
        try {
            snapshot = await this.snapshot(repository, snapshotController.signal);
        }
        catch (error) {
            if (!isAbort(error)) {
                this.onError(repository, error);
                this.onStatus(repository, "error");
            }
            return;
        }
        if (!force && (state.active?.key === snapshot.dedupeKey || state.lastCompletedKey === snapshot.dedupeKey)) {
            return;
        }
        if (state.active?.key === snapshot.dedupeKey) {
            await state.active.promise;
            return;
        }
        state.active?.controller.abort();
        const controller = new AbortController();
        this.onStatus(repository, "reviewing");
        const promise = this.review(repository, snapshot, controller.signal);
        state.active = { key: snapshot.dedupeKey, controller, promise };
        try {
            await promise;
            if (state.active?.promise === promise && !controller.signal.aborted) {
                state.lastCompletedKey = snapshot.dedupeKey;
                this.onStatus(repository, "completed");
            }
        }
        catch (error) {
            if (!controller.signal.aborted && !isAbort(error)) {
                this.onError(repository, error);
                this.onStatus(repository, "error");
            }
        }
        finally {
            if (state.active?.promise === promise) {
                state.active = undefined;
            }
        }
    }
    state(repository) {
        let state = this.states.get(repository);
        if (state === undefined) {
            state = {};
            this.states.set(repository, state);
        }
        return state;
    }
}
exports.AutoReviewScheduler = AutoReviewScheduler;
function isAbort(error) {
    return error instanceof Error && error.name === "AbortError";
}
