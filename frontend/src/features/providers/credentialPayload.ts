import type { BuiltinProvider, CredentialInput } from "../../contracts/generated";

/** Only visible fields enter the request; empty edit secrets preserve stored values. */
export function buildCredentialPayload(
  provider: Pick<BuiltinProvider, "fields" | "credential_groups">,
  name: string,
  inputs: Record<string, string>,
  group: number,
  clearSecrets: string[] = []
): CredentialInput {
  const selected = provider.credential_groups[group];
  const groupedKeys = new Set(provider.credential_groups.flat());
  const result: CredentialInput = { name: name.trim(), values: {}, secrets: {}, clear_secrets: [] };
  for (const field of provider.fields) {
    if (field.secret) {
      if (selected && groupedKeys.has(field.key) && !selected.includes(field.key)) continue;
      if (clearSecrets.includes(field.key)) result.clear_secrets.push(field.key);
      else if (inputs[field.key]) result.secrets[field.key] = inputs[field.key];
    } else {
      result.values[field.key] = inputs[field.key] ?? "";
    }
  }
  return result;
}
