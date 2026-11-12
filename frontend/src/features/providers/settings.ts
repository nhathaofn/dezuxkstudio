import type { ProviderCapability, SettingSpec } from "../../contracts/generated";

export function filterSettingSpecs(
  specs: SettingSpec[],
  capabilities: ProviderCapability[]
): SettingSpec[] {
  return specs.filter((spec) => !spec.capability || capabilities.includes(spec.capability));
}

/** Only fields present in the draft participate; an empty string resets a stored override. */
export function settingsDiff(
  settings: Record<string, number>,
  draft: Record<string, string>
): Record<string, string> {
  return Object.fromEntries(
    Object.entries(draft).flatMap(([key, raw]) => {
      const value = raw.trim();
      if (
        value === ""
          ? settings[key] === undefined
          : settings[key] !== undefined && Number(value) === settings[key]
      )
        return [];
      return [[key, value]];
    })
  );
}
