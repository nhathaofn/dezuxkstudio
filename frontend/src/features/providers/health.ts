import type { ProviderHealth, ProvidersListResult } from "../../contracts/generated";

export function healthStatus(health: ProviderHealth): "untested" | "ok" | "dead" | "unknown" {
  if (!health.tested) return "untested";
  if (health.dead) return "dead";
  return health.outcome === "ok" ? "ok" : "unknown";
}

/** Uses the full snapshot so capability filters never hide a broken active key. */
export function deadKeyNames(data: ProvidersListResult | null): string[] {
  if (!data) return [];
  return [
    ...data.builtins.flatMap((provider) =>
      provider.credentials
        .filter((credential) => credential.active && healthStatus(credential.health) === "dead")
        .map((credential) => `${provider.display_name} (${credential.name})`)
    ),
    ...data.custom
      .filter((provider) => healthStatus(provider.health) === "dead")
      .map((provider) => provider.name),
  ];
}

export function relativeTestTime(
  testedAt: string,
  language: string,
  now = Date.now()
): string | null {
  const timestamp = Date.parse(testedAt);
  if (!Number.isFinite(timestamp)) return null;
  const seconds = (timestamp - now) / 1000;
  // Calendar approximations suffice for displaying the age of a key test.
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 365 * 24 * 60 * 60],
    ["month", 30 * 24 * 60 * 60],
    ["day", 24 * 60 * 60],
    ["hour", 60 * 60],
    ["minute", 60],
    ["second", 1],
  ];
  const [unit, size] =
    units.find(([, size]) => Math.abs(seconds) >= size) ?? units[units.length - 1];
  return new Intl.RelativeTimeFormat(language, { numeric: "auto" }).format(
    Math.round(seconds / size),
    unit
  );
}

/** Outcomes raised by the app itself: their translated label says everything, and the server's
 *  English detail would only leak untranslated text into the UI. */
const LOCAL_OUTCOMES: ReadonlySet<string> = new Set(["secret_missing", "invalid_credentials"]);

/** Whether a test result's server-side detail text may be shown to the user. */
export const showsTestDetail = (outcome: string) => !LOCAL_OUTCOMES.has(outcome);
