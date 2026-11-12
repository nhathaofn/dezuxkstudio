import type { ProviderHealth } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { healthStatus, relativeTestTime } from "./health";

export function HealthBadge({ health }: { health: ProviderHealth }) {
  const t = useT();
  const language = usePreferencesStore((s) => s.prefs.language);
  const status = healthStatus(health);
  if (status === "untested") return <Badge>{t("providers.health.untested")}</Badge>;
  if (status === "ok") {
    const relativeTime = relativeTestTime(health.tested_at, language);
    return (
      <Badge className="providers-health--ok">
        {t("providers.health.ok")}
        {relativeTime && (
          <>
            {" "}
            ·{" "}
            <time dateTime={health.tested_at} title={health.tested_at}>
              {relativeTime}
            </time>
          </>
        )}
      </Badge>
    );
  }
  const outcome = t(`providers.outcome.${health.outcome}`);
  const relativeTime = relativeTestTime(health.tested_at, language);
  return (
    <details className={`providers-health providers-health--${status}`}>
      <summary title={[outcome, health.detail].filter(Boolean).join(": ")}>
        <Badge>
          {t(`providers.health.${status}`)} · {outcome}
          {relativeTime && (
            <>
              {" "}
              ·{" "}
              <time dateTime={health.tested_at} title={health.tested_at}>
                {relativeTime}
              </time>
            </>
          )}
        </Badge>
      </summary>
      {health.detail && <p>{health.detail}</p>}
    </details>
  );
}
