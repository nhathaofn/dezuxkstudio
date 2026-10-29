import {
  Actions,
  Events,
  type ActionMap,
  type ActionName,
  type ActionProgress,
  type EventMap,
  type EventName,
} from "../../contracts/generated";
import { appConfig } from "../../config/appConfig";
import { ConnectionManager, type ConnectionStatus } from "./connection";
import type { Endpoint } from "./endpoint";
import { ActionError, ClientErrorCodes } from "./errors";
import { PendingRequests } from "./pending";
import type { ServerFrame } from "./protocol";

export interface RequestOptions {
  /** Aborting sends a cancel frame; the server cancels the action's context. */
  signal?: AbortSignal;
  onProgress?: (p: ActionProgress) => void;
}

type Listener<T> = (data: T) => void;

let sequence = 0;
const nextId = (): string => `${Date.now().toString(36)}-${(++sequence).toString(36)}`;

/** Typed realtime client: every action and event is checked against the contracts. */
export class RealtimeClient {
  private readonly pending = new PendingRequests();
  private readonly listeners = new Map<string, Set<Listener<unknown>>>();
  private readonly statusListeners = new Set<Listener<ConnectionStatus>>();
  private readonly conn: ConnectionManager;
  private actionTimeoutMs: number = appConfig.fallback.actionTimeoutMs;

  constructor() {
    this.conn = new ConnectionManager({
      onFrame: (f) => this.dispatch(f),
      onStatus: (s) => this.statusListeners.forEach((l) => l(s)),
      onClosed: () =>
        this.pending.rejectAll(
          new ActionError(ClientErrorCodes.DISCONNECTED, "connection to the server was lost")
        ),
    });
  }

  connect(endpoint: Endpoint): void {
    this.actionTimeoutMs = endpoint.actionTimeoutMs;
    this.conn.connect(endpoint.url, endpoint.token);
  }

  disconnect(): void {
    this.conn.disconnect();
  }

  getStatus(): ConnectionStatus {
    return this.conn.getStatus();
  }

  onStatus(listener: Listener<ConnectionStatus>): () => void {
    this.statusListeners.add(listener);
    return () => this.statusListeners.delete(listener);
  }

  /** Subscribes to a server event. Always call the returned function on unmount. */
  on<E extends EventName>(event: E, listener: Listener<EventMap[E]>): () => void {
    let set = this.listeners.get(event);
    if (!set) this.listeners.set(event, (set = new Set()));
    set.add(listener as Listener<unknown>);
    return () => set.delete(listener as Listener<unknown>);
  }

  request<A extends ActionName>(
    action: A,
    payload: ActionMap[A]["payload"],
    opts: RequestOptions = {}
  ): Promise<ActionMap[A]["result"]> {
    return this.send(action, payload, opts) as Promise<ActionMap[A]["result"]>;
  }

  /** Untyped variant for developer tools (action tester). */
  requestRaw(action: string, payload: unknown, opts: RequestOptions = {}): Promise<unknown> {
    return this.send(action, payload, opts);
  }

  async ping(): Promise<number> {
    const start = performance.now();
    await this.request(Actions.SYSTEM_PING, {});
    return Math.round(performance.now() - start);
  }

  private send(action: string, payload: unknown, opts: RequestOptions): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (opts.signal?.aborted) {
        reject(new ActionError("ERR_CANCELLED", "request cancelled"));
        return;
      }
      const id = nextId();
      const cancelRemote = () => this.conn.send(JSON.stringify({ type: "cancel", id }));
      const onAbort = () => {
        cancelRemote();
        this.pending.reject(id, new ActionError("ERR_CANCELLED", "request cancelled"));
      };

      this.pending.add(
        id,
        this.actionTimeoutMs + appConfig.clientTimeoutGraceMs,
        {
          resolve: (v) => {
            opts.signal?.removeEventListener("abort", onAbort);
            resolve(v);
          },
          reject: (e) => {
            opts.signal?.removeEventListener("abort", onAbort);
            reject(e);
          },
          onProgress: opts.onProgress,
        },
        cancelRemote
      );

      if (!this.conn.send(JSON.stringify({ type: "action", id, action, payload }))) {
        this.pending.reject(
          id,
          new ActionError(ClientErrorCodes.NOT_CONNECTED, "not connected to the server")
        );
        return;
      }
      opts.signal?.addEventListener("abort", onAbort, { once: true });
    });
  }

  private dispatch(frame: ServerFrame): void {
    if (frame.type === "response") {
      this.pending.complete(frame);
      return;
    }
    if (frame.event === Events.ACTION_PROGRESS && frame.ref_id) {
      this.pending.progress(frame.ref_id, frame.data as ActionProgress);
    }
    this.listeners.get(frame.event)?.forEach((l) => l(frame.data));
  }
}

/** Process-wide client instance. */
export const rtClient = new RealtimeClient();
