import { EventBus } from "./eventBus";
import { ActionDispatcher } from "./actionDispatcher";
import { ConnectionManager } from "./connection";
import { resolveDynamicServerUrl, saveTargetUrl } from "./urlResolver";
import type { ConnectionStatus, EventHandler, StatusListener } from "./types";

export * from "./types";

export class RealtimeClient {
  private bus = new EventBus();
  private dispatcher = new ActionDispatcher();
  private conn: ConnectionManager;

  constructor() {
    this.conn = new ConnectionManager("", this.dispatcher, this.bus);
  }

  public async init(explicitUrl?: string): Promise<string> {
    const url = explicitUrl || (await resolveDynamicServerUrl());
    if (url) {
      this.conn.setUrl(url);
    }
    return url;
  }

  public setTargetUrl(url: string): void {
    saveTargetUrl(url);
    this.conn.setUrl(url);
  }

  public getTargetUrl(): string {
    return this.conn.getUrl();
  }

  public onStatusChange(listener: StatusListener): () => void {
    return this.conn.onStatus(listener);
  }

  public getStatus(): ConnectionStatus {
    return this.conn.getStatus();
  }

  public sendAction<T = unknown>(action: string, payload?: unknown): Promise<T> {
    return new Promise((resolve, reject) => {
      const { id, raw } = this.dispatcher.createRequest(action, payload);
      if (!this.conn.send(raw)) {
        return reject(new Error("WebSocket is not connected"));
      }
      this.dispatcher.register(id, resolve as (val: unknown) => void, reject);
    });
  }

  public on(event: string, handler: EventHandler): () => void {
    return this.bus.on(event, handler);
  }

  public async ping(): Promise<number> {
    const start = performance.now();
    await this.sendAction("system:ping");
    return Math.round(performance.now() - start);
  }

  public disconnect(): void {
    this.conn.disconnect();
  }
}

export const rtClient = new RealtimeClient();
