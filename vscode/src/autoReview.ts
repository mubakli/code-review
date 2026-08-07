export interface ScheduledSnapshot {
  reviewId: string;
  dedupeKey: string;
}

export type SchedulerStatus = "idle" | "scheduled" | "reviewing" | "completed" | "error";

interface RepositoryState {
  timer?: NodeJS.Timeout;
  active?: {
    key: string;
    controller: AbortController;
    promise: Promise<void>;
  };
  lastCompletedKey?: string;
}

export class AutoReviewScheduler {
  private readonly states = new Map<string, RepositoryState>();
  private disposed = false;

  constructor(
    private debounceMs: number,
    private readonly snapshot: (repository: string, signal: AbortSignal) => Promise<ScheduledSnapshot>,
    private readonly review: (repository: string, snapshot: ScheduledSnapshot, signal: AbortSignal) => Promise<void>,
    private readonly onStatus: (repository: string, status: SchedulerStatus) => void = () => {},
    private readonly onError: (repository: string, error: unknown) => void = () => {}
  ) {}

  setDebounce(milliseconds: number): void {
    this.debounceMs = milliseconds;
  }

  notify(repository: string, debounceMs = this.debounceMs): void {
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

  async runNow(repository: string): Promise<void> {
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

  dispose(): void {
    this.disposed = true;
    for (const state of this.states.values()) {
      if (state.timer !== undefined) {
        clearTimeout(state.timer);
      }
      state.active?.controller.abort();
    }
    this.states.clear();
  }

  private async evaluate(repository: string, force: boolean): Promise<void> {
    const state = this.state(repository);
    const snapshotController = new AbortController();
    let snapshot: ScheduledSnapshot;
    try {
      snapshot = await this.snapshot(repository, snapshotController.signal);
    } catch (error) {
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
    } catch (error) {
      if (!controller.signal.aborted && !isAbort(error)) {
        this.onError(repository, error);
        this.onStatus(repository, "error");
      }
    } finally {
      if (state.active?.promise === promise) {
        state.active = undefined;
      }
    }
  }

  private state(repository: string): RepositoryState {
    let state = this.states.get(repository);
    if (state === undefined) {
      state = {};
      this.states.set(repository, state);
    }
    return state;
  }
}

function isAbort(error: unknown): boolean {
  return error instanceof Error && error.name === "AbortError";
}
