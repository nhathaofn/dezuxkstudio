import type { EventHandler } from "./types";

export class EventBus {
  private listeners = new Map<string, Set<EventHandler>>();

  public on(event: string, handler: EventHandler): () => void {
    if (!this.listeners.has(event)) {
      this.listeners.set(event, new Set());
    }
    this.listeners.get(event)!.add(handler);
    return () => {
      this.listeners.get(event)?.delete(handler);
    };
  }

  public dispatch(event: string, data: unknown, refId?: string): void {
    if (this.listeners.has(event)) {
      this.listeners.get(event)!.forEach((handler) => handler(data, refId));
    }
  }
}
