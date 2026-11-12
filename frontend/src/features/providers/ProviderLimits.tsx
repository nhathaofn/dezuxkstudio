import { useId, useState } from "react";
import type { ProviderCapability } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT, type MessageKey } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { filterSettingSpecs, settingsDiff } from "./settings";
import { useProvidersStore } from "./useProvidersStore";

export function ProviderLimits({
  ownerId,
  capabilities,
  settings,
  defaults,
  disabled,
}: {
  ownerId: string;
  capabilities: ProviderCapability[];
  settings: Record<string, number>;
  defaults: Record<string, number>;
  disabled: boolean;
}) {
  const t = useT();
  const id = useId();
  const specs = useProvidersStore((s) => s.data?.setting_specs);
  const update = useProvidersStore((s) => s.updateSettings);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [error, setError] = useState<ActionError | null>(null);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const visible = filterSettingSpecs(specs ?? [], capabilities);
  const patch = settingsDiff(
    settings,
    Object.fromEntries(
      visible.filter((spec) => spec.key in draft).map((spec) => [spec.key, draft[spec.key]])
    )
  );
  return (
    <section aria-label={t("providers.limits")}>
      <form
        className="providers-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy || disabled) return;
          setBusy(true);
          setError(null);
          setSaved(false);
          try {
            await update(ownerId, patch);
            setDraft({});
            setSaved(true);
          } catch (err) {
            setError(toActionError(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <ErrorText error={error} />
        <fieldset className="providers-fieldset providers-limits-list" disabled={busy || disabled}>
          {(["concurrency", "rateLimits"] as const).map((group) => (
            <section key={group}>
              <h3>{t(`providers.${group}`)}</h3>
              {visible
                .filter((spec) => spec.key.endsWith("_max_workers") === (group === "concurrency"))
                .map((spec) => {
                  const label = t(`providers.setting.${spec.key}` as MessageKey);
                  const invalid =
                    error?.code === "ERR_VALIDATION" && error.details?.field === spec.key;
                  return (
                    <div className="providers-limit-row" key={spec.key}>
                      <div className="providers-limit-info">
                        <label htmlFor={`${id}-${spec.key}`}>{label}</label>
                        <p id={`${id}-${spec.key}-help`} className="subtitle">
                          {t(`providers.setting.${spec.key}.help` as MessageKey)}
                        </p>
                      </div>
                      <div className="providers-limit-controls">
                        <span className="providers-meta">
                          {t("providers.defaultValue", { value: defaults[spec.key] ?? "—" })}
                        </span>
                        <input
                          id={`${id}-${spec.key}`}
                          type="number"
                          step={1}
                          min={spec.min}
                          max={spec.max}
                          className="input-control"
                          placeholder={String(defaults[spec.key] ?? "")}
                          aria-invalid={invalid}
                          aria-describedby={`${id}-${spec.key}-help`}
                          value={
                            draft[spec.key] ??
                            (settings[spec.key] === undefined ? "" : String(settings[spec.key]))
                          }
                          onChange={(e) => {
                            setDraft({ ...draft, [spec.key]: e.target.value });
                            setSaved(false);
                          }}
                        />
                        {(draft[spec.key] ??
                          (settings[spec.key] === undefined ? "" : String(settings[spec.key]))) !==
                          "" && (
                          <Button
                            type="button"
                            aria-label={t("providers.resetSetting", { setting: label })}
                            title={t("providers.resetSetting", { setting: label })}
                            variant="ghost"
                            onClick={() => {
                              setDraft({ ...draft, [spec.key]: "" });
                              setSaved(false);
                            }}
                          >
                            {t("providers.reset")}
                          </Button>
                        )}
                      </div>
                      {invalid && (
                        <p role="alert" className="providers-danger">
                          {t("providers.settingError", { min: spec.min, max: spec.max })}
                        </p>
                      )}
                    </div>
                  );
                })}
            </section>
          ))}
        </fieldset>
        {!!Object.keys(patch).length && (
          <div className="providers-save-footer">
            <span>{t("providers.unsaved")}</span>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setDraft({});
                setError(null);
              }}
            >
              {t("providers.discard")}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={busy || disabled || !Object.keys(patch).length}
            >
              {t(busy ? "providers.saving" : "providers.save")}
            </Button>
          </div>
        )}
        {saved && (
          <p role="status" className="providers-success">
            {t("providers.saved")}
          </p>
        )}
      </form>
    </section>
  );
}
