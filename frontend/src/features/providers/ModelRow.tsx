import { memo } from "react";
import { Pencil, Star } from "lucide-react";
import type { ProviderModel } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Switch } from "../../components/ui/Switch";
import { useT } from "../../lib/i18n";
import { factsSummary } from "./modelPresentation";

interface Props {
  model: ProviderModel;
  index: number;
  disabled: boolean;
  showEndpoint: boolean;
  onChange: (index: number, patch: Partial<ProviderModel>) => void;
  onEdit: (index: number) => void;
}

/**
 * One model line. Memoised: toggling a model re-renders that row (and the one that lost the default
 * star), not the whole list. All handlers it receives are stable, so props only change with the data.
 */
export const ModelRow = memo(function ModelRow({
  model,
  index,
  disabled,
  showEndpoint,
  onChange,
  onEdit,
}: Props) {
  const t = useT();
  const summary = factsSummary(model.facts, {
    seconds: t("providers.seconds"),
    audio: t("providers.audio"),
    tokens: (count) => t("providers.tokens", { count }),
  });
  const defaultLabel = t("providers.defaultFor", {
    capability: t(`providers.capability.${model.capability}`),
  });
  return (
    <div className={`providers-model-row${model.enabled ? "" : " providers-model-row--disabled"}`}>
      <Switch
        checked={model.enabled}
        disabled={disabled}
        aria-label={t("providers.enabledRow", { row: index + 1 })}
        onCheckedChange={(enabled) => onChange(index, { enabled })}
      />
      <div
        className="providers-model-name"
        title={[model.display_name, model.model_id].filter(Boolean).join(" · ")}
      >
        <span>{model.display_name || model.model_id}</span>
        {model.display_name && model.display_name !== model.model_id && (
          <code>{model.model_id}</code>
        )}
      </div>
      {showEndpoint && (
        <span className="providers-endpoint" title={model.endpoint}>
          {model.endpoint}
        </span>
      )}
      <span className="providers-model-summary providers-meta" title={summary}>
        {summary}
      </span>
      <Button
        type="button"
        variant="ghost"
        iconOnly
        disabled={disabled || !model.enabled}
        title={defaultLabel}
        aria-label={defaultLabel}
        aria-pressed={model.is_default}
        className={model.is_default ? "providers-default-star" : ""}
        onClick={() => onChange(index, { is_default: true })}
      >
        <Star size={14} fill={model.is_default ? "currentColor" : "none"} />
      </Button>
      <Button
        type="button"
        variant="ghost"
        iconOnly
        title={t("providers.editModel")}
        aria-label={t("providers.editModel")}
        onClick={() => onEdit(index)}
      >
        <Pencil size={14} />
      </Button>
    </div>
  );
});
