import { AUTH_SUBPROTOCOL_PREFIX, PROTOCOL_VERSION } from "../../contracts/generated";
import { appConfig } from "../../config/appConfig";
import { backoffDelay } from "./backoff";
import { parseServerFrame, type ServerFrame } from "./protocol";

export type ConnectionStatus = "idle" | "connecting" | "connected" | "reconnecting";

interface Handlers {
  onFrame: (frame: ServerFrame) => void;
  onStatus: (status: ConnectionStatus) => void;
  onClosed: () => void;
}

/**
 * Owns one WebSocket with automatic reconnection. The token travels as a
 * subprotocol, never in the URL.
 */
export class ConnectionManager {
  private ws: WebSocket | null = null;
  private attempts = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private target: { url: string; token: string } | null = null;
  private status: ConnectionStatus = "idle";

  constructor(private readonly handlers: Handlers) {}

  getStatus(): ConnectionStatus {
    return this.status;
  }

  connect(url: string, token: string): void {
    this.disconnect();
    this.target = { url, token };
    this.attempts = 0;
    this.open();
  }

  disconnect(): void {
    this.target = null;
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onclose = null;
      ws.close();
      this.handlers.onClosed();
    }
    this.setStatus("idle");
  }

  send(data: string): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(data);
    return true;
  }

  private open(): void {
    if (!this.target) return;
    this.setStatus(this.attempts > 0 ? "reconnecting" : "connecting");

    const protocols = [PROTOCOL_VERSION];
    if (this.target.token) protocols.push(AUTH_SUBPROTOCOL_PREFIX + this.target.token);

    let ws: WebSocket;
    try {
      ws = new WebSocket(this.target.url, protocols);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      this.attempts = 0;
      this.setStatus("connected");
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data !== "string") return;
      const frame = parseServerFrame(ev.data);
      if (frame) this.handlers.onFrame(frame);
      else console.warn("[realtime] dropped malformed frame");
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.handlers.onClosed();
      this.scheduleReconnect();
    };
  }

  private scheduleReconnect(): void {
    if (!this.target) return;
    this.setStatus("reconnecting");
    const delay = backoffDelay(this.attempts++, appConfig.reconnect);
    this.timer = setTimeout(() => this.open(), delay);
  }

  private setStatus(s: ConnectionStatus): void {
    if (s === this.status) return;
    this.status = s;
    this.handlers.onStatus(s);
  }
}
