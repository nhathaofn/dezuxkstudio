import { Fragment, useId, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import type { CustomEndpoint, ProviderCapability, ProviderModel } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { emptyFacts, newModel, parseDurations, parseResolutions } from "./customModels";

function FactsEditor({
  model,
  onChange,
  invalid,
}: {
  model: ProviderModel;
  onChange: (facts: ProviderModel["facts"]) => void;
  invalid: boolean;
}) {
  const t = useT();
  const id = useId();
  const [errors, setErrors] = useState<Record<string, boolean>>({});
  const list = (field: "durations" | "resolutions") => (
    <div className="form-group">
      <label htmlFor={`${id}-${field}`}>{t(`providers.facts.${field}`)}</label>
      <input
        id={`${id}-${field}`}
        className="input-control"
        defaultValue={model.facts[field].join(", ")}
        aria-invalid={invalid || !!errors[field]}
        onInvalid={(e) => {
          const details = e.currentTarget.closest("details");
          if (details) details.open = true;
        }}
        onChange={(e) => {
          try {
            const value =
              field === "durations"
                ? parseDurations(e.target.value)
                : parseResolutions(e.target.value);
            onChange({ ...model.facts, [field]: value });
            e.target.setCustomValidity("");
            setErrors((previous) => ({ ...previous, [field]: false }));
          } catch {
            e.target.setCustomValidity(t(`providers.facts.${field}Error`));
            setErrors((previous) => ({ ...previous, [field]: true }));
          }
        }}
      />
      {errors[field] && (
        <p className="providers-danger" role="alert">
          {t(`providers.facts.${field}Error`)}
        </p>
      )}
    </div>
  );
  return (
    <details className="providers-details">
      <summary>{t("providers.facts")}</summary>
      <div className="providers-form-grid">
        {model.capability === "video" && list("durations")}
        {(model.capability === "image" || model.capability === "video") && list("resolutions")}
        {model.capability === "video" && (
          <label>
            <input
              type="checkbox"
              checked={model.facts.audio}
              onChange={(e) => onChange({ ...model.facts, audio: e.target.checked })}
            />
            {t("providers.facts.audio")}
          </label>
        )}
        {model.capability === "llm" && (
          <div className="form-group">
            <label htmlFor={`${id}-tokens`}>{t("providers.facts.maxOutputTokens")}</label>
            <input
              id={`${id}-tokens`}
              type="number"
              min={0}
              step={1}
              className="input-control"
              value={model.facts.max_output_tokens || ""}
              aria-invalid={invalid}
              onInvalid={(e) => {
                const details = e.currentTarget.closest("details");
                if (details) details.open = true;
              }}
              onChange={(e) =>
                onChange({ ...model.facts, max_output_tokens: Number(e.target.value) })
              }
            />
          </div>
        )}
      </div>
    </details>
  );
}

/** Controlled editor shared by built-in details and the custom-provider form. */
export function ModelList({
  models,
  onChange,
  capabilities,
  endpoints,
  error,
  disabled = false,
}: {
  models: ProviderModel[];
  onChange: (models: ProviderModel[]) => void;
  capabilities: ProviderCapability[];
  /** Already filtered to the custom provider's chosen protocol; absent for built-ins. */
  endpoints?: CustomEndpoint[];
  error?: ActionError | null;
  disabled?: boolean;
}) {
  const t = useT();
  const id = useId();
  const [modelId, setModelId] = useState("");
  const [selection, setSelection] = useState("");
  const [duplicate, setDuplicate] = useState(false);
  const selected = endpoints
    ? (endpoints.find((entry) => entry.key === selection)?.key ?? endpoints[0]?.key ?? "")
    : (capabilities.find((capability) => capability === selection) ?? capabilities[0] ?? "");
  const invalid = error?.details?.field === "models";
  const factsInvalid = error?.details?.field === "facts";
  const change = (index: number, patch: Partial<ProviderModel>) => {
    const updated = { ...models[index], ...patch };
    if (!updated.enabled) updated.is_default = false;
    if (updated.capability !== models[index].capability) {
      updated.facts = emptyFacts();
      updated.is_default = false;
    }
    onChange(
      models.map((model, i) =>
        i === index
          ? updated
          : updated.is_default && model.capability === updated.capability
            ? { ...model, is_default: false }
            : model
      )
    );
  };
  return (
    <fieldset className="providers-fieldset" disabled={disabled}>
      <legend>{t("providers.models")}</legend>
      {invalid && (
        <p role="alert" className="providers-danger">
          {t("providers.modelsError")}
        </p>
      )}
      {factsInvalid && (
        <p role="alert" className="providers-danger">
          {t("providers.factsError")}
        </p>
      )}
      <div
        className="providers-models"
        role="region"
        aria-label={t("providers.models")}
        tabIndex={0}
      >
        <table className="providers-model-table">
          <thead>
            <tr>
              {(
                [
                  "enabled",
                  "default",
                  "displayName",
                  "modelId",
                  "capability",
                  ...(endpoints ? ["endpoint" as const] : []),
                  "actions",
                ] as const
              ).map((key) => (
                <th key={key}>{t(`providers.${key}`)}</th>
              ))}
            </tr>
          </thead>
          {[...new Set(models.map((model) => model.capability))].map((capability) => (
            <tbody key={capability}>
              <tr>
                <th colSpan={endpoints ? 7 : 6} scope="rowgroup">
                  {t(`providers.capability.${capability}`)}
                </th>
              </tr>
              {models.map((model, index) =>
                model.capability !== capability ? null : (
                  <Fragment key={`${index}-${model.model_id}-${model.capability}`}>
                    <tr>
                      <td>
                        <input
                          type="checkbox"
                          checked={model.enabled}
                          aria-label={t("providers.enabledRow", { row: index + 1 })}
                          onChange={(e) => change(index, { enabled: e.target.checked })}
                        />
                      </td>
                      <td>
                        <input
                          type="radio"
                          name={`${id}-${capability}`}
                          checked={model.is_default}
                          disabled={!model.enabled}
                          aria-label={t("providers.defaultRow", { row: index + 1 })}
                          onChange={() => change(index, { is_default: true })}
                        />
                      </td>
                      <td>
                        <input
                          className="input-control"
                          value={model.display_name}
                          placeholder={model.model_id}
                          aria-label={t("providers.displayNameRow", { row: index + 1 })}
                          aria-invalid={invalid}
                          onChange={(e) => change(index, { display_name: e.target.value })}
                        />
                      </td>
                      <td>
                        <code title={model.model_id}>{model.model_id}</code>
                        {model.user_added && <Badge>{t("providers.userAdded")}</Badge>}
                      </td>
                      <td>
                        <Badge>{t(`providers.capability.${model.capability}`)}</Badge>
                      </td>
                      {endpoints && (
                        <td>
                          <select
                            className="input-control"
                            value={model.endpoint}
                            aria-invalid={invalid}
                            aria-label={t("providers.endpointRow", { row: index + 1 })}
                            onChange={(e) => {
                              const endpoint = endpoints.find(
                                (entry) => entry.key === e.target.value
                              );
                              if (endpoint)
                                change(index, {
                                  endpoint: endpoint.key,
                                  capability: endpoint.capability,
                                });
                            }}
                          >
                            {endpoints.map((entry) => (
                              <option key={entry.key} value={entry.key}>
                                {t("providers.endpointOption", {
                                  endpoint: entry.key,
                                  capability: t(`providers.capability.${entry.capability}`),
                                })}
                              </option>
                            ))}
                          </select>
                        </td>
                      )}
                      <td>
                        {model.user_added && (
                          <Button
                            type="button"
                            iconOnly
                            aria-label={t("providers.removeModelRow", { row: index + 1 })}
                            title={t("providers.removeModelRow", { row: index + 1 })}
                            onClick={() => onChange(models.filter((_, i) => i !== index))}
                          >
                            <Trash2 size={14} />
                          </Button>
                        )}
                      </td>
                    </tr>
                    {model.capability !== "tts" && (
                      <tr>
                        <td colSpan={endpoints ? 7 : 6}>
                          <FactsEditor
                            model={model}
                            invalid={factsInvalid}
                            onChange={(facts) => change(index, { facts })}
                          />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              )}
            </tbody>
          ))}
        </table>
        {!models.length && <p className="subtitle">{t("providers.noModels")}</p>}
      </div>
      <div className="providers-add-model">
        <label htmlFor={`${id}-new`}>{t("providers.modelId")}</label>
        <input
          id={`${id}-new`}
          className="input-control"
          value={modelId}
          onChange={(e) => {
            setModelId(e.target.value);
            setDuplicate(false);
          }}
          aria-invalid={invalid || duplicate}
        />
        <label htmlFor={`${id}-selection`}>
          {t(endpoints ? "providers.endpoint" : "providers.capability")}
        </label>
        <select
          id={`${id}-selection`}
          className="input-control"
          value={selected}
          onChange={(e) => {
            setSelection(e.target.value);
            setDuplicate(false);
          }}
        >
          {endpoints
            ? endpoints.map((entry) => (
                <option key={entry.key} value={entry.key}>
                  {t("providers.endpointOption", {
                    endpoint: entry.key,
                    capability: t(`providers.capability.${entry.capability}`),
                  })}
                </option>
              ))
            : capabilities.map((capability) => (
                <option key={capability} value={capability}>
                  {t(`providers.capability.${capability}`)}
                </option>
              ))}
        </select>
        <Button
          type="button"
          disabled={!modelId.trim() || !selected}
          onClick={() => {
            const endpoint = endpoints?.find((entry) => entry.key === selected);
            const capability = endpoint?.capability ?? (selected as ProviderCapability);
            if (
              models.some(
                (model) => model.model_id === modelId.trim() && model.capability === capability
              )
            ) {
              setDuplicate(true);
              return;
            }
            onChange([
              ...models,
              {
                ...newModel({
                  model_id: modelId.trim(),
                  capability,
                  endpoint: endpoint?.key ?? ("" as ProviderModel["endpoint"]),
                }),
                is_default: !models.some(
                  (model) => model.capability === capability && model.enabled && model.is_default
                ),
              },
            ]);
            setModelId("");
          }}
        >
          <Plus size={14} />
          {t("providers.addModel")}
        </Button>
      </div>
      {duplicate && (
        <p className="providers-danger" role="alert">
          {t("providers.duplicateModel")}
        </p>
      )}
    </fieldset>
  );
}
