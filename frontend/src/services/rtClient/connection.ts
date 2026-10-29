import { EventMessageSchema } from "../../schemas/realtime.schema";
import type { ConnectionStatus, StatusListener } from "./types";
import type { ActionDispatcher } from "./actionDispatcher";
import type { EventBus } from "./eventBus";

export class ConnectionManager {
  private ws: WebSocket | null = null;
  private url: string;
  private status: ConnectionStatus = "disconnected";
  private statusListeners = new Set<StatusListener>();
  private reconnectAttempts = 0;
  private reconnectTimer: number | null = null;

  constructor(
    initialUrl: string,
    private dispatcher: ActionDispatcher,
    private bus: EventBus
  ) {
    this.url = initialUrl;
  }

  public getStatus(): ConnectionStatus {
    return this.status;
  }

  public getUrl(): string {
    return this.url;
  }

  public setUrl(newUrl: string): void {
    this.url = newUrl;
    if (this.ws) {
      this.ws.close();
    }
    if (this.url) {
      this.connect();
    }
  }

  public onStatus(listener: StatusListener): () => void {
    this.statusListeners.add(listener);
    listener(this.status);
    return () => this.statusListeners.delete(listener);
  }

  private setStatus(s: ConnectionStatus): void {
    this.status = s;
    this.statusListeners.forEach((fn) => fn(s));
  }

  public connect(): void {
    if (!this.url) {
      this.setStatus("disconnected");
      return;
    }

    if (
      this.ws &&
      (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }

    this.setStatus(this.reconnectAttempts > 0 ? "reconnecting" : "connecting");

    try {
      this.ws = new WebSocket(this.url);

      this.ws.onopen = () => {
        this.reconnectAttempts = 0;
        this.setStatus("connected");
      };

      this.ws.onmessage = (event) => {
        try {
          const raw = JSON.parse(event.data);
          // Try handling as action response
          if (this.dispatcher.handleResponse(raw)) return;

          // Validate as server push event
          const parsedEvent = EventMessageSchema.safeParse(raw);
          if (parsedEvent.success) {
            this.bus.dispatch(
              parsedEvent.data.event,
              parsedEvent.data.data,
              parsedEvent.data.ref_id
            );
          }
        } catch (e) {
          console.error("[ConnectionManager] Malformed payload:", e);
        }
      };

      this.ws.onclose = () => {
        this.dispatcher.rejectAll(new Error("WebSocket disconnected"));
        this.setStatus("disconnected");
        this.scheduleReconnect();
      };

      this.ws.onerror = (err) => {
        console.warn("[ConnectionManager] WebSocket socket warning:", err);
      };
    } catch {
      this.setStatus("disconnected");
      this.scheduleReconnect();
    }
  }

  public send(data: string): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return false;
    }
    this.ws.send(data);
    return true;
  }

  private scheduleReconnect(): void {
    if (!this.url) return;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    const delay = Math.min(1000 * Math.pow(1.5, this.reconnectAttempts), 5000);
    this.reconnectAttempts++;
    this.reconnectTimer = window.setTimeout(() => this.connect(), delay);
  }

  public disconnect(): void {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.dispatcher.rejectAll(new Error("Client requested disconnect"));
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.setStatus("disconnected");
  }
}
