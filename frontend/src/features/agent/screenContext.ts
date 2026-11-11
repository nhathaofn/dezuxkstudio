import { useEffect } from "react";
import { create } from "zustand";
import type { AgentUIContextPayload } from "../../contracts/generated";

/**
 * What the current screen tells the agent about itself. Every screen uses the same shape, so adding a
 * feature screen only needs one `useAgentScreenContext(...)` call — no agent-specific backend code.
 * - `screen`: human-readable screen name
 * - `feature`: workflow node type the screen drives (e.g. "tool.watermark_remover")
 * - `selected_files`: files/folders loaded or selected on the screen
 * - `settings`: current settings, keyed by the feature's param names
 * - `last_result`: short summary of the latest run on the screen
 */
export type AgentScreenContext = Omit<AgentUIContextPayload, "route" | "note">;

interface ScreenContextState {
  context: AgentScreenContext | null;
  setContext: (ctx: AgentScreenContext | null) => void;
}

export const useScreenContextStore = create<ScreenContextState>((set) => ({
  context: null,
  setContext: (context) => set({ context }),
}));

/** Publishes the calling screen's context to the agent while the screen is mounted. */
export function useAgentScreenContext(ctx: AgentScreenContext): void {
  const key = JSON.stringify(ctx);
  useEffect(() => {
    useScreenContextStore.getState().setContext(JSON.parse(key) as AgentScreenContext);
  }, [key]);
  useEffect(() => () => useScreenContextStore.getState().setContext(null), []);
}
