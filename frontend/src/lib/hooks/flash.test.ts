import { afterEach, describe, expect, it, vi } from "vitest";
import { Flash } from "./flash";

afterEach(() => vi.useRealTimers());

describe("Flash", () => {
  it("shows a value, resets it after the delay, and restarts on re-show", () => {
    vi.useFakeTimers();
    const seen: (string | null)[] = [];
    const f = new Flash<string>(100, (v) => seen.push(v));
    f.show("a");
    vi.advanceTimersByTime(80);
    f.show("b"); // restarts the delay
    vi.advanceTimersByTime(80);
    expect(seen).toEqual(["a", "b"]);
    vi.advanceTimersByTime(30);
    expect(seen).toEqual(["a", "b", null]);
  });

  it("dispose cancels the pending reset (unmounted component)", () => {
    vi.useFakeTimers();
    const onChange = vi.fn();
    const f = new Flash<boolean>(100, onChange);
    f.show(true);
    f.dispose();
    vi.advanceTimersByTime(500);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("clear resets immediately", () => {
    vi.useFakeTimers();
    const seen: (boolean | null)[] = [];
    const f = new Flash<boolean>(100, (v) => seen.push(v));
    f.show(true);
    f.clear();
    vi.advanceTimersByTime(500);
    expect(seen).toEqual([true, null]);
  });
});
