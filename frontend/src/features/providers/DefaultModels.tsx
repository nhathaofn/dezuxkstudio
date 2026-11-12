import { useId, useState } from "react";
import { SlidersHorizontal, TriangleAlert } from "lucide-react";
import type { PurposeDefault } from "../../contracts/generated";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { useProvidersStore } from "./useProvidersStore";

function PurposeRow({
  value,
  disabled,
  onConfigure,
}: {
  value: PurposeDefault;
  disabled: boolean;
  onConfigure: () => void;
}) {
  const t = useT();
  const id = useId();
  const setDefault = useProvidersStore((s) => s.setDefault);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ActionError | null>(null);
  const effective = value.candidates.find((candidate) => candidate.ref === value.effective);
  const label = effective
    ? `${effective.provider_name} / ${effective.model.display_name || effective.model.model_id}`
    : t("providers.nothingUsable");
  const providers = [...new Set(value.candidates.map((candidate) => candidate.provider_id))];
  return (
    <div className="form-group">
      <label htmlFor={id}>{t(`providers.purpose.${value.purpose}`)}</label>
      <select
        id={id}
        className="input-control"
        value={value.chosen}
        disabled={busy || disabled}
        aria-invalid={error?.details?.field === "model"}
        onChange={async (e) => {
          setBusy(true);
          setError(null);
          try {
            await setDefault(value.purpose, e.target.value);
          } catch (err) {
            setError(toActionError(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <option value="">{t("providers.autoModel", { model: label })}</option>
        {value.chosen_unavailable && (
          <option value={value.chosen} disabled>
            {t("providers.unavailableModel", { model: value.chosen })}
          </option>
        )}
        {providers.map((providerId) => {
          const candidates = value.candidates.filter(
            (candidate) => candidate.provider_id === providerId
          );
          return (
            <optgroup key={providerId} label={candidates[0].provider_name}>
              {candidates.map((candidate) => (
                <option key={candidate.ref} value={candidate.ref}>
                  {candidate.is_provider_default
                    ? t("providers.providerDefaultModel", {
                        model: candidate.model.display_name || candidate.model.model_id,
                      })
                    : candidate.model.display_name || candidate.model.model_id}
                </option>
              ))}
            </optgroup>
          );
        })}
      </select>
      {value.chosen_unavailable && (
        <p className="providers-notice providers-notice--warning" role="status">
          <TriangleAlert size={14} />
          {t("providers.chosenUnavailable", { model: label })}
        </p>
      )}
      {!value.candidates.length && (
        <p className="subtitle">
          {t("providers.noCandidates")}{" "}
          <a
            href="#providers-configure"
            onClick={(e) => {
              e.preventDefault();
              onConfigure();
            }}
          >
            {t("providers.configureProvider")}
          </a>
        </p>
      )}
      <ErrorText error={error} />
      {error?.details?.field === "model" && (
        <p role="alert" className="providers-danger">
          {t("providers.defaultModelError")}
        </p>
      )}
    </div>
  );
}

export function DefaultModels({
  disabled,
  onConfigure,
}: {
  disabled: boolean;
  onConfigure: () => void;
}) {
  const t = useT();
  const defaults = useProvidersStore((s) => s.defaults);
  return (
    <Card icon={SlidersHorizontal} title={t("providers.defaultModels")}>
      {defaults ? (
        <div className="providers-form-grid">
          {defaults.purposes.map((purpose) => (
            <PurposeRow
              key={purpose.purpose}
              value={purpose}
              disabled={disabled}
              onConfigure={onConfigure}
            />
          ))}
        </div>
      ) : (
        <p className="subtitle">{t("providers.loadingDefaults")}</p>
      )}
    </Card>
  );
}
