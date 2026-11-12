import { useId, useState } from "react";
import { TriangleAlert } from "lucide-react";
import type { PurposeDefault } from "../../contracts/generated";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { useProvidersStore } from "./useProvidersStore";
import { CapabilityIcon } from "./CapabilityIcon";

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
    <tr>
      <th scope="row">
        <label htmlFor={id}>
          <CapabilityIcon value={value.purpose === "text" ? "llm" : value.purpose} />
          {t(`providers.purpose.${value.purpose}`)}
        </label>
      </th>
      <td>
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
      </td>
      <td>
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
              href="?tab=providers&sub=providers"
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
      </td>
    </tr>
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
    <section aria-label={t("providers.defaultModels")}>
      {defaults ? (
        <table className="providers-default-table">
          <thead>
            <tr>
              <th>{t("providers.purposeColumn")}</th>
              <th>{t("providers.modelTab")}</th>
              <th>{t("providers.statusColumn")}</th>
            </tr>
          </thead>
          <tbody>
            {defaults.purposes.map((purpose) => (
              <PurposeRow
                key={purpose.purpose}
                value={purpose}
                disabled={disabled}
                onConfigure={onConfigure}
              />
            ))}
          </tbody>
        </table>
      ) : (
        <p className="subtitle">{t("providers.loadingDefaults")}</p>
      )}
    </section>
  );
}
