import React, { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { AlertTriangle, Cpu } from "lucide-react";
import type { ProviderPurpose } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";
import { useProvidersStore } from "../providers/useProvidersStore";
import { resolvePurposeDefault } from "./types";

interface MediaModelSelectProps {
  purpose: ProviderPurpose;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}

export const MediaModelSelect: React.FC<MediaModelSelectProps> = ({
  purpose,
  value,
  onChange,
  disabled,
}) => {
  const t = useT();
  const navigate = useNavigate();
  const defaults = useProvidersStore((s) => s.defaults);
  const fetchDefaults = useProvidersStore((s) => s.fetchDefaults);

  useEffect(() => {
    if (!defaults) {
      void fetchDefaults();
    }
  }, [defaults, fetchDefaults]);

  const entry = resolvePurposeDefault(defaults, purpose);
  const candidates = entry?.candidates ?? [];
  const effectiveCandidate = entry?.effective
    ? candidates.find((c) => c.ref === entry.effective)
    : undefined;
  const effectiveLabel = effectiveCandidate
    ? `${effectiveCandidate.provider_name} · ${effectiveCandidate.model.display_name || effectiveCandidate.model.model_id}`
    : t("providers.nothingUsable");

  const hasUnavailableSelection = Boolean(value && !candidates.some((c) => c.ref === value));

  return (
    <div className="form-group">
      <label
        className="field-label watermark-label-with-icon"
        htmlFor={`mediagen-model-${purpose}`}
      >
        <Cpu size={13} />
        <span>{t("mediagen.field.model")}</span>
      </label>
      <select
        id={`mediagen-model-${purpose}`}
        className="input-control"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
      >
        <option value="">{t("providers.autoModel", { model: effectiveLabel })}</option>
        {hasUnavailableSelection && (
          <option value={value}>{t("providers.unavailableModel", { model: value })}</option>
        )}
        {candidates.map((c) => (
          <option key={c.ref} value={c.ref}>
            {c.provider_name} — {c.model.display_name || c.model.model_id}
          </option>
        ))}
      </select>

      {entry && candidates.length === 0 && (
        <div className="mediagen-provider-warning" role="status">
          <AlertTriangle size={14} className="mediagen-provider-warning__icon" />
          <span className="mediagen-provider-warning__text">{t("providers.noCandidates")}</span>
          <Button variant="ghost" size="sm" onClick={() => navigate("/settings?tab=providers")}>
            {t("providers.configureProvider")}
          </Button>
        </div>
      )}
    </div>
  );
};
