import type {
  CustomEndpoint,
  ProviderModel,
  ProviderDiscoveryFormat,
} from "../../contracts/generated";

/** Reconcile rows when their protocol/endpoint changes, retaining at most one enabled default per capability. */
export function reconcileModels(
  models: ProviderModel[],
  endpoints: CustomEndpoint[],
  protocol: ProviderDiscoveryFormat
): ProviderModel[] {
  const allowed = endpoints.filter((endpoint) => endpoint.formats.includes(protocol));
  const defaults = new Set<string>();
  return models.map((model) => {
    const endpoint = allowed.find((entry) => entry.key === model.endpoint) ?? allowed[0];
    const isDefault =
      !!endpoint && model.enabled && model.is_default && !defaults.has(endpoint.capability);
    if (isDefault && endpoint) defaults.add(endpoint.capability);
    const capability = endpoint?.capability ?? model.capability;
    return {
      ...model,
      endpoint: endpoint?.key ?? model.endpoint,
      capability,
      facts: capability === model.capability ? model.facts : emptyFacts(),
      is_default: isDefault,
    };
  });
}

export function emptyFacts(): ProviderModel["facts"] {
  return { durations: [], resolutions: [], audio: false, max_output_tokens: 0 };
}

export function newModel(
  model: Pick<ProviderModel, "model_id" | "capability" | "endpoint">
): ProviderModel {
  return {
    ...model,
    display_name: "",
    enabled: true,
    is_default: false,
    user_added: true,
    facts: emptyFacts(),
  };
}

/** Identity follows the backend: model id plus capability, independent of endpoint. */
export function mergeDiscovered(
  existing: ProviderModel[],
  discovered: Pick<ProviderModel, "model_id" | "capability" | "endpoint">[]
): ProviderModel[] {
  const result = [...existing];
  const seen = new Set(existing.map((m) => `${m.model_id}\0${m.capability}`));
  const defaults = new Set(
    existing.filter((m) => m.enabled && m.is_default).map((m) => m.capability)
  );
  for (const entry of discovered) {
    const key = `${entry.model_id}\0${entry.capability}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push({
      ...newModel(entry),
      ...entry,
      enabled: true,
      is_default: false,
      user_added: false,
    });
  }
  return result.map((model) => {
    if (!model.enabled || defaults.has(model.capability)) return model;
    defaults.add(model.capability);
    return { ...model, is_default: true };
  });
}

export function parseDurations(value: string): number[] {
  if (!value.trim()) return [];
  return value.split(",").map((part) => {
    const n = Number(part.trim());
    if (!part.trim() || !Number.isSafeInteger(n) || n <= 0) throw new Error("durations");
    return n;
  });
}

export function parseResolutions(value: string): string[] {
  if (!value.trim()) return [];
  const values = value.split(",").map((part) => part.trim());
  if (values.some((part) => !part)) throw new Error("resolutions");
  return values;
}
