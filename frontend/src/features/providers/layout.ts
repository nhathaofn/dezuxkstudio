import type {
  BuiltinProvider,
  CustomProvider,
  ProviderCapability,
  ProviderHealth,
  ProvidersListResult,
} from "../../contracts/generated";
import { healthStatus } from "./health";

export type ProviderEntry =
  { kind: "builtin"; provider: BuiltinProvider } | { kind: "custom"; provider: CustomProvider };
export type ProviderStatus = "ok" | "dead" | "unverified" | "unconfigured";
export const providerName = (entry: ProviderEntry) =>
  entry.kind === "builtin" ? entry.provider.display_name : entry.provider.name;
export function activeHealth(entry: ProviderEntry): ProviderHealth | undefined {
  return entry.kind === "builtin"
    ? entry.provider.credentials.find((key) => key.active)?.health
    : entry.provider.health;
}
export function providerStatus(entry: ProviderEntry): ProviderStatus {
  const health = activeHealth(entry);
  if (!health)
    return entry.kind === "builtin" && !entry.provider.configured ? "unconfigured" : "unverified";
  const status = healthStatus(health);
  return status === "ok" || status === "dead" ? status : "unverified";
}
export function providerGroups(
  data: ProvidersListResult | null,
  search = "",
  capability: "all" | ProviderCapability = "all"
) {
  const entries: ProviderEntry[] = [
    ...(data?.builtins.map((provider): ProviderEntry => ({ kind: "builtin", provider })) ?? []),
    ...(data?.custom.map((provider): ProviderEntry => ({ kind: "custom", provider })) ?? []),
  ];
  const filtered = entries
    .filter(
      (entry) =>
        `${providerName(entry)} ${entry.provider.id}`
          .toLocaleLowerCase()
          .includes(search.trim().toLocaleLowerCase()) &&
        (capability === "all" || entry.provider.capabilities.includes(capability))
    )
    .sort(
      (a, b) =>
        providerName(a).localeCompare(providerName(b)) || a.provider.id.localeCompare(b.provider.id)
    );
  return {
    attention: filtered.filter((entry) => providerStatus(entry) === "dead"),
    configured: filtered.filter(
      (entry) =>
        entry.kind === "builtin" && entry.provider.configured && providerStatus(entry) !== "dead"
    ),
    custom: filtered.filter((entry) => entry.kind === "custom" && providerStatus(entry) !== "dead"),
    unconfigured: filtered.filter(
      (entry) => entry.kind === "builtin" && !entry.provider.configured
    ),
  };
}
export function parseProviderLocation(params: URLSearchParams) {
  return {
    sub: params.get("sub") === "defaults" ? ("defaults" as const) : ("providers" as const),
    providerId: params.get("p") ?? "",
  };
}
export function providerLocation(
  params: URLSearchParams,
  patch: { sub?: "defaults" | "providers"; providerId?: string }
) {
  const next = new URLSearchParams(params);
  if (patch.sub !== undefined) next.set("sub", patch.sub);
  if (patch.providerId !== undefined) {
    if (patch.providerId) next.set("p", patch.providerId);
    else next.delete("p");
  }
  return next;
}
export function initialProvider(
  data: ProvidersListResult | null,
  requested: string
): ProviderEntry | undefined {
  const groups = providerGroups(data);
  const all = [...groups.attention, ...groups.configured, ...groups.custom, ...groups.unconfigured];
  return (
    all.find((entry) => entry.provider.id === requested) ??
    groups.configured[0] ??
    groups.attention[0] ??
    groups.custom[0]
  );
}
