import type { ActionProgress } from "../../contracts/generated";
import { ActionError } from "./errors";
import type { ResponseFrame } from "./protocol";

interface Entry {
  resolve: (data: unknown) => void;
  reject: (err: ActionError) => void;
  onProgress?: (p: ActionProgress) => void;
  timer: ReturnType<typeof setTimeout>;
}

/** Tracks in-flight requests: correlation, timeout, progress and bulk rejection. */
export class PendingRequests {
  private entries = new Map<string, Entry>();

  get size(): number {
    return this.entries.size;
  }

  add(
    id: string,
    timeoutMs: number,
    handlers: Pick<Entry, "resolve" | "reject" | "onProgress">,
    onTimeout: () => void
  ): void {
    const timer = setTimeout(() => {
      if (this.settle(id)) {
        onTimeout();
        handlers.reject(new ActionError("ERR_TIMEOUT", "no response from server in time"));
      }
    }, timeoutMs);
    this.entries.set(id, { ...handlers, timer });
  }

  /** Resolves or rejects the matching request. Returns false for unknown ids. */
  complete(frame: ResponseFrame): boolean {
    const entry = this.entries.get(frame.id);
    if (!entry || !this.settle(frame.id)) return false;
    if (frame.success) entry.resolve(frame.data ?? {});
    else
      entry.reject(
        frame.error
          ? ActionError.fromBody(frame.error)
          : new ActionError("ERR_INTERNAL", "request failed")
      );
    return true;
  }

  progress(id: string, p: ActionProgress): void {
    this.entries.get(id)?.onProgress?.(p);
  }

  reject(id: string, err: ActionError): void {
    const entry = this.entries.get(id);
    if (entry && this.settle(id)) entry.reject(err);
  }

  rejectAll(err: ActionError): void {
    for (const [id, entry] of [...this.entries]) {
      if (this.settle(id)) entry.reject(err);
    }
  }

  private settle(id: string): boolean {
    const entry = this.entries.get(id);
    if (!entry) return false;
    clearTimeout(entry.timer);
    this.entries.delete(id);
    return true;
  }
}
