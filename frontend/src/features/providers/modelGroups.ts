import { createContext } from "react";
import type { ProviderCapability } from "../../contracts/generated";

/** true = the user opened this group. Groups are closed by default. */
export type ModelGroups = Partial<Record<ProviderCapability, boolean>>;

/** Owned by the settings view so switching providers preserves each provider's open groups. */
export const ModelGroupsContext = createContext<{
  expanded: ModelGroups;
  toggle: (capability: ProviderCapability) => void;
} | null>(null);

/** Groups are closed until the user opens them; a search shows every group that has a match. */
export function isGroupOpen(
  expanded: ModelGroups,
  capability: ProviderCapability,
  searching: boolean
): boolean {
  return searching || !!expanded[capability];
}
