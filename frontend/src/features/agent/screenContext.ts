import { useEffect } from "react";
import { create } from "zustand";
import type { AgentUIContextPayload, ProviderDefaultsResult } from "../../contracts/generated";
import { safeStorage } from "../../lib/storage";
import { useProvidersStore } from "../providers/useProvidersStore";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import { useAgentStore } from "./useAgentStore";

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

export interface AskAgentAssetParams {
  assetId?: string;
  filePath?: string;
  fileName?: string;
  mimeType?: string;
}

/**
 * 1-click cross action helper:
 * Opens the Agent drawer, switches to the chat tab, and attaches structured asset context
 * into the prompt composer.
 */
export function askAgentAboutMediaAsset(params: AskAgentAssetParams): void {
  const drawer = useAgentDrawerStore.getState();
  drawer.openPanel();
  drawer.setActiveTab("chat");
  drawer.setChatView("chat");

  const tag = params.assetId
    ? `[media_asset asset_id="${params.assetId}"]`
    : params.filePath
      ? `[media_asset path="${params.filePath}"]`
      : "[media_asset]";

  const currentPrompt = useAgentStore.getState().prompt;
  const promptAddition = `${tag}\nPlease inspect this media asset.`;
  const nextPrompt = currentPrompt.trim()
    ? `${currentPrompt}\n\n${promptAddition}`
    : promptAddition;

  useAgentStore.getState().setPrompt(nextPrompt);
}

export type StudioDelegatedStage = "script_normalize" | "extract_assets" | "author_shots";

export interface DelegateStudioStageParams {
  projectId: string;
  episodeId: string;
  stage: StudioDelegatedStage;
  prompt: string;
}

/**
 * Returns true if an explicit text model override is set or Settings -> Providers has a usable
 * PurposeText candidate/effective model configured.
 */
export function isTextProviderConfigured(
  defaults: ProviderDefaultsResult | null,
  modelOverride?: string
): boolean {
  if (modelOverride && modelOverride.trim() !== "") return true;
  const textPurpose = defaults?.purposes.find((p) => p.purpose === "text");
  return Boolean(
    textPurpose && (textPurpose.candidates.length > 0 || textPurpose.effective.trim() !== "")
  );
}

/**
 * Fallback helper when no PurposeText provider is configured:
 * opens the Agent Drawer chat tab and pre-fills the composer prompt for user inspection (never auto-sends).
 */
export function delegateStudioStageToAgent(params: DelegateStudioStageParams): void {
  const drawer = useAgentDrawerStore.getState();
  drawer.openPanel();
  drawer.setActiveTab("chat");
  drawer.setChatView("chat");
  useAgentStore.getState().setPrompt(params.prompt);
}

/** Returns the active billing source display string for an external agent provider. */
export function getActiveBillingSource(providerId: string, compatibleProviders?: string[]): string {
  const linkedKey = safeStorage.get(`agent_provider_link:${providerId}`);
  if (!linkedKey) return "OAuth / Subscription";
  const builtins = useProvidersStore.getState().data?.builtins ?? [];
  const [targetProviderId, credId] = linkedKey.split(":");
  if (compatibleProviders && !compatibleProviders.includes(targetProviderId)) {
    return "OAuth / Subscription";
  }
  for (const b of builtins) {
    if (b.id === targetProviderId) {
      const cred = b.credentials?.find((c) => c.id === credId);
      if (cred) {
        return `Linked API Key: ${cred.name}`;
      }
    }
  }
  return "OAuth / Subscription";
}
