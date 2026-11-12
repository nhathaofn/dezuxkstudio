import { createContext } from "react";
import type { ProviderCapability } from "../../contracts/generated";

export type ModelGroups = Partial<Record<ProviderCapability, boolean>>;

/** Owned by the settings view so switching providers preserves each provider's groups. */
export const ModelGroupsContext = createContext<{
  collapsed: ModelGroups;
  toggle: (capability: ProviderCapability) => void;
} | null>(null);
