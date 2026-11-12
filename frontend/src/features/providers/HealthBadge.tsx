import type { ProviderHealth } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { healthStatus, relativeTestTime } from "./health";
export function HealthBadge({ health }: { health: ProviderHealth }) {
  const t = useT();
  const language = usePreferencesStore((s) => s.prefs.language);
  const status = healthStatus(health);
  const label = t(
    `providers.status.${status === "ok" || status === "dead" ? status : "unverified"}`
  );
  const detail =
    status === "ok"
      ? relativeTestTime(health.tested_at, language)
      : status === "untested"
        ? ""
        : t(`providers.outcome.${health.outcome}`);
  return (
    <span
      className={`providers-health providers-health--${status}`}
      title={[
        label,
        status === "untested" || status === "unknown" ? t(`providers.health.${status}`) : "",
        health.detail,
        health.tested_at,
      ]
        .filter(Boolean)
        .join(" · ")}
    >
      <span className={`providers-dot providers-dot--${status}`} />
      <span className="providers-truncate">
        {label}
        {detail && ` ${status === "ok" ? "·" : "—"} ${detail}`}
      </span>
    </span>
  );
}
