import { afterEach, describe, expect, it, vi } from "vitest";
import { rtClient } from "../../lib/rtClient";
import { ActionError } from "../../lib/rtClient/errors";
import { integerChoices, useNodeSpecsStore } from "./useNodeSpecs";

describe("useNodeSpecsStore", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    useNodeSpecsStore.setState({ specs: null, loading: false, error: null });
  });

  // A screen mounted before the socket connected fails its first load; the reload that bootstrap
  // runs on connect (load(true)) must then fill the specs so the settings are no longer disabled.
  it("recovers when the first load ran before the connection was up", async () => {
    const spy = vi
      .spyOn(rtClient, "request")
      .mockRejectedValueOnce(new ActionError("ERR_NOT_CONNECTED", "not connected"))
      .mockResolvedValueOnce({ specs: [{ type: "tool.watermark_remover", params: [] }] } as never);

    await useNodeSpecsStore.getState().load();
    expect(useNodeSpecsStore.getState().specs).toBeNull();
    expect(useNodeSpecsStore.getState().error).not.toBeNull();

    await useNodeSpecsStore.getState().load(true);
    expect(useNodeSpecsStore.getState().specs?.[0].type).toBe("tool.watermark_remover");
    expect(useNodeSpecsStore.getState().error).toBeNull();
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("a forced load wins over a stale one still in flight", async () => {
    let rejectStale: (e: unknown) => void = () => {};
    vi.spyOn(rtClient, "request")
      .mockImplementationOnce(() => new Promise((_, rej) => (rejectStale = rej)))
      .mockResolvedValueOnce({ specs: [{ type: "x", params: [] }] } as never);
    const stale = useNodeSpecsStore.getState().load();
    await useNodeSpecsStore.getState().load(true);
    rejectStale(new ActionError("ERR_NOT_CONNECTED", "not connected"));
    await stale;
    expect(useNodeSpecsStore.getState().specs?.[0].type).toBe("x");
    expect(useNodeSpecsStore.getState().error).toBeNull();
  });

  it("loads once unless forced", async () => {
    const spy = vi.spyOn(rtClient, "request").mockResolvedValue({ specs: [] } as never);
    await useNodeSpecsStore.getState().load();
    await useNodeSpecsStore.getState().load();
    expect(spy).toHaveBeenCalledTimes(1);
    await useNodeSpecsStore.getState().load(true);
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("derives integer choices from the spec range", () => {
    expect(integerChoices({ name: "w", label: "", kind: "integer", min: 0, max: 64 })).toEqual([
      0, 1, 2, 4, 8, 16, 32, 64,
    ]);
    expect(integerChoices({ name: "w", label: "", kind: "integer", min: 1, max: 12 })).toEqual([
      1, 2, 4, 8, 12,
    ]);
    expect(integerChoices(undefined)).toEqual([]);
  });
});
