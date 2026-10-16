import { SessionCoordinator, StaleSessionError } from "./session";

// The retained Go/WASM singleton has no proven unInit export. A second login
// must use a fresh document, even when it is the same user after logout.
export class SdkRuntimeBoundary {
  private initializedGeneration?: number;
  private restarting = false;
  constructor(
    private session: SessionCoordinator,
    private restart: () => void,
    private shutdown: () => Promise<unknown>,
    private shutdownTimeoutMs = 5000,
  ) {
    session.onChange(() => {
      if (
        this.initializedGeneration !== undefined &&
        this.initializedGeneration !== session.generation
      )
        this.scheduleRestart();
    });
  }
  beforeLogin(): void {
    if (this.restarting || this.initializedGeneration !== undefined) {
      this.scheduleRestart();
      throw new StaleSessionError();
    }
    this.initializedGeneration = this.session.generation;
  }
  private scheduleRestart() {
    if (this.restarting) return;
    this.restarting = true;
    // clear() emits before enqueueing its storage removal. Defer one microtask
    // so both clear and commit are fully queued before waiting for persistence.
    queueMicrotask(async () => {
      await this.session.settled();
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          Promise.resolve()
            .then(this.shutdown)
            .catch(() => undefined),
          new Promise<void>((resolve) => {
            timer = setTimeout(resolve, this.shutdownTimeoutMs);
          }),
        ]);
      } finally {
        clearTimeout(timer);
        this.restart();
      }
    });
  }
}
