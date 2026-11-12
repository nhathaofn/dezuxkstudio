import type {
  CustomEndpoint,
  CustomModelInput,
  ProviderDiscoveryFormat,
} from "../../contracts/generated";

/** Reconcile rows when their protocol/endpoint changes, retaining at most one enabled default per capability. */
export function reconcileModels(
  models: CustomModelInput[],
  endpoints: CustomEndpoint[],
  protocol: ProviderDiscoveryFormat
): CustomModelInput[] {
  const allowed = endpoints.filter((endpoint) => endpoint.formats.includes(protocol));
  const defaults = new Set<string>();
  return models.map((model) => {
    const endpoint = allowed.find((entry) => entry.key === model.endpoint) ?? allowed[0];
    const isDefault =
      !!endpoint && model.enabled && model.is_default && !defaults.has(endpoint.capability);
    if (isDefault && endpoint) defaults.add(endpoint.capability);
    return { ...model, endpoint: endpoint?.key ?? model.endpoint, is_default: isDefault };
  });
}
