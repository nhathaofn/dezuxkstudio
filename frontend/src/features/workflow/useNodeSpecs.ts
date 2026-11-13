import { useEffect } from "react";
import { create } from "zustand";
import { Actions, type Param, type Spec } from "../../contracts/generated";
import { rtClient, toActionError, type ActionError } from "../../lib/rtClient";

interface NodeSpecsState {
  specs: Spec[] | null;
  loading: boolean;
  error: ActionError | null;
  /** Loads workflow:specs once; later calls reuse the result (pass force to refetch). */
  load: (force?: boolean) => Promise<void>;
}

let loadSeq = 0;

/** The backend's node specs: the single source of a feature's settings (options, min/max, defaults). */
export const useNodeSpecsStore = create<NodeSpecsState>((set, get) => ({
  specs: null,
  loading: false,
  error: null,
  load: async (force = false) => {
    // A forced load (bootstrap on every connect) always runs and supersedes one still in flight,
    // which may have been sent before the socket was up.
    if (!force && (get().loading || get().specs)) return;
    const seq = ++loadSeq;
    set({ loading: true, error: null });
    try {
      const res = await rtClient.request(Actions.WORKFLOW_SPECS, {});
      if (seq === loadSeq) set({ specs: res.specs ?? [], loading: false });
    } catch (err) {
      if (seq === loadSeq) set({ error: toActionError(err), loading: false });
    }
  },
}));

export function findParam(spec: Spec | undefined, name: string): Param | undefined {
  return spec?.params.find((p) => p.name === name);
}

/** Choices for an integer setting: 0 (auto) when allowed, then powers of two up to the spec's max. */
export function integerChoices(param: Param | undefined): number[] {
  if (!param) return [];
  const min = param.min ?? 0;
  const max = param.max ?? min;
  const out: number[] = [];
  if (min <= 0 && max >= 0) out.push(0);
  for (let v = Math.max(1, min); v <= max; v *= 2) out.push(v);
  if (max > 0 && !out.includes(max)) out.push(max);
  return out;
}

/** Returns the spec of one node type, loading the specs on first use. */
export function useNodeSpec(type: string): Spec | undefined {
  const specs = useNodeSpecsStore((s) => s.specs);
  const load = useNodeSpecsStore((s) => s.load);
  useEffect(() => {
    void load();
  }, [load]);
  return specs?.find((s) => s.type === type);
}
