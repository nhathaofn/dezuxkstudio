import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appConfig } from "../../config/appConfig";
import { ConnectionManager } from "./connection";

class FakeSocket {
  static OPEN = 1;
  static instances: FakeSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  constructor(
    public url: string,
    public protocols: string[]
  ) {
    FakeSocket.instances.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.closed = true;
  }
  open() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
  }
}

describe("ConnectionManager half-open detection", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.instances = [];
    vi.stubGlobal("WebSocket", FakeSocket);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const { checkIntervalMs, idleMs, timeoutMs } = appConfig.liveness;

  it("pings a silent server, then drops the socket and reconnects when nothing answers", () => {
    const onClosed = vi.fn();
    const statuses: string[] = [];
    const cm = new ConnectionManager({
      onFrame: () => {},
      onStatus: (s) => statuses.push(s),
      onClosed,
    });
    cm.connect("ws://h/ws", "");
    const first = FakeSocket.instances[0];
    first.open();

    vi.advanceTimersByTime(idleMs + checkIntervalMs);
    expect(first.sent.some((f) => f.includes('"system:ping"'))).toBe(true);
    expect(onClosed).not.toHaveBeenCalled();

    vi.advanceTimersByTime(timeoutMs + checkIntervalMs);
    expect(first.closed).toBe(true);
    expect(onClosed).toHaveBeenCalledTimes(1);
    expect(statuses).toContain("reconnecting");

    vi.advanceTimersByTime(appConfig.reconnect.maxDelayMs);
    expect(FakeSocket.instances.length).toBe(2);
    cm.disconnect();
  });

  it("keeps a socket that answers the ping", () => {
    const onClosed = vi.fn();
    const cm = new ConnectionManager({ onFrame: () => {}, onStatus: () => {}, onClosed });
    cm.connect("ws://h/ws", "");
    const ws = FakeSocket.instances[0];
    ws.open();

    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(idleMs + checkIntervalMs);
      ws.onmessage?.({ data: '{"type":"response","id":"liveness-x","success":true}' });
    }
    expect(onClosed).not.toHaveBeenCalled();
    expect(ws.closed).toBe(false);
    cm.disconnect();
  });

  it("survives a socket that throws on send at the moment of the ping", () => {
    const onClosed = vi.fn();
    const cm = new ConnectionManager({ onFrame: () => {}, onStatus: () => {}, onClosed });
    cm.connect("ws://h/ws", "");
    const ws = FakeSocket.instances[0];
    ws.open();
    ws.send = () => {
      throw new Error("InvalidStateError");
    };
    expect(() => vi.advanceTimersByTime(idleMs + checkIntervalMs)).not.toThrow();
    // No answer to the (failed) probe: the watchdog still drops the socket.
    vi.advanceTimersByTime(timeoutMs + checkIntervalMs);
    expect(onClosed).toHaveBeenCalledTimes(1);
    cm.disconnect();
  });
});
