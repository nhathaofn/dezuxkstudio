import { useContext, useId, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ChevronDown, ChevronRight, Pencil, Plus, Search, Star, Trash2 } from "lucide-react";
import type { CustomEndpoint, ProviderCapability, ProviderModel } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { Switch } from "../../components/ui/Switch";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { emptyFacts, newModel, parseDurations, parseResolutions } from "./customModels";
import { enabledCounts, factsSummary, filterModels } from "./modelPresentation";
import { CapabilityIcon } from "./CapabilityIcon";
import { ModelGroupsContext, type ModelGroups } from "./modelGroups";

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
    <section className="providers-details">
      <h3>{t("providers.facts")}</h3>
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
              onChange={(e) =>
                onChange({ ...model.facts, max_output_tokens: Number(e.target.value) })
              }
            />
          </div>
        )}
      </div>
    </section>
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
  toolbar,
}: {
  models: ProviderModel[];
  onChange: (models: ProviderModel[]) => void;
  capabilities: ProviderCapability[];
  endpoints?: CustomEndpoint[];
  error?: ActionError | null;
  disabled?: boolean;
  toolbar?: ReactNode;
}) {
  const t = useT();
  const id = useId();
  const [search, setSearch] = useState("");
  const groupState = useContext(ModelGroupsContext);
  const [localCollapsed, setLocalCollapsed] = useState<ModelGroups>({});
  const collapsed = groupState?.collapsed ?? localCollapsed;
  const toggleGroup = (capability: ProviderCapability) => {
    if (groupState) groupState.toggle(capability);
    else setLocalCollapsed((previous) => ({ ...previous, [capability]: !previous[capability] }));
  };
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<{ index: number; model: ProviderModel } | null>(null);
  const [modelId, setModelId] = useState("");
  const [selection, setSelection] = useState("");
  const [duplicate, setDuplicate] = useState(false);
  const selected = endpoints
    ? (endpoints.find((entry) => entry.key === selection)?.key ?? endpoints[0]?.key ?? "")
    : (capabilities.find((value) => value === selection) ?? capabilities[0] ?? "");
  const filtered = filterModels(models, search);
  const counts = enabledCounts(filtered);
  const change = (index: number, patch: Partial<ProviderModel>) => {
    const updated = { ...models[index], ...patch };
    if (!updated.enabled) updated.is_default = false;
    if (updated.capability !== models[index].capability) {
      if (!patch.facts) updated.facts = emptyFacts();
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
  const bulk = (enabled: boolean) =>
    onChange(
      models.map((model) =>
        filtered.includes(model)
          ? { ...model, enabled, is_default: enabled && model.is_default }
          : model
      )
    );
  return (
    <fieldset className="providers-fieldset providers-model-editor" disabled={disabled}>
      <div className="providers-model-toolbar">
        <label className="providers-model-search">
          <Search size={14} />
          <input
            className="input-control"
            type="search"
            value={search}
            placeholder={t("providers.searchModels")}
            aria-label={t("providers.searchModels")}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <div className="providers-model-toolbar-actions">
          {toolbar}
          <Button type="button" onClick={() => setAdding(true)}>
            <Plus size={14} />
            {t("providers.addModel")}
          </Button>
        </div>
        <div className="providers-model-toolbar-sub">
          <span className="providers-meta">{t("providers.enabledCount", counts)}</span>
          <Button
            type="button"
            variant="ghost"
            disabled={!filtered.length}
            onClick={() => bulk(true)}
          >
            {t("providers.enableAll")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={!filtered.length}
            onClick={() => bulk(false)}
          >
            {t("providers.disableAll")}
          </Button>
        </div>
      </div>
      {(error?.details?.field === "models" || error?.details?.field === "facts") && (
        <p className="providers-danger" role="alert">
          {t(error.details.field === "facts" ? "providers.factsError" : "providers.modelsError")}
        </p>
      )}
      <div className="providers-models">
        {[...new Set(filtered.map((model) => model.capability))].map((capability) => {
          const rows = filtered.filter((model) => model.capability === capability);
          return (
            <section className="providers-model-group" key={capability}>
              <button
                type="button"
                className="providers-model-group-heading"
                aria-expanded={!collapsed[capability]}
                onClick={() => toggleGroup(capability)}
              >
                <CapabilityIcon value={capability} />
                <strong>{t(`providers.capability.${capability}`)}</strong>
                <span className="providers-meta">
                  {t("providers.enabledCount", enabledCounts(rows))}
                </span>
                {collapsed[capability] ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
              </button>
              {!collapsed[capability] &&
                rows.map((model) => {
                  const index = models.indexOf(model);
                  const summary = factsSummary(model.facts, {
                    seconds: t("providers.seconds"),
                    audio: t("providers.audio"),
                    tokens: (count) => t("providers.tokens", { count }),
                  });
                  const defaultLabel = t("providers.defaultFor", {
                    capability: t(`providers.capability.${capability}`),
                  });
                  return (
                    <div
                      className={`providers-model-row${model.enabled ? "" : " providers-model-row--disabled"}`}
                      key={`${model.model_id}-${capability}`}
                    >
                      <Switch
                        checked={model.enabled}
                        disabled={disabled}
                        aria-label={t("providers.enabledRow", { row: index + 1 })}
                        onCheckedChange={(enabled) => change(index, { enabled })}
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
                      {endpoints && (
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
                        onClick={() => change(index, { is_default: true })}
                      >
                        <Star size={14} fill={model.is_default ? "currentColor" : "none"} />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        iconOnly
                        title={t("providers.editModel")}
                        aria-label={t("providers.editModel")}
                        onClick={() =>
                          setEditing({ index, model: { ...model, facts: { ...model.facts } } })
                        }
                      >
                        <Pencil size={14} />
                      </Button>
                    </div>
                  );
                })}
            </section>
          );
        })}
        {!filtered.length && (
          <p className="subtitle">{t(models.length ? "providers.empty" : "providers.noModels")}</p>
        )}
      </div>
      {editing &&
        createPortal(
          <Dialog
            title={t("providers.editModel")}
            onClose={() => setEditing(null)}
            dirty={JSON.stringify(editing.model) !== JSON.stringify(models[editing.index])}
            footer={(requestClose) => (
              <>
                <Button type="button" onClick={requestClose}>
                  {t("providers.cancel")}
                </Button>
                <Button type="submit" form={id + "-edit"} variant="primary" disabled={disabled}>
                  {t("providers.save")}
                </Button>
              </>
            )}
          >
            <form
              id={id + "-edit"}
              className="providers-form"
              onSubmit={(e) => {
                e.preventDefault();
                e.stopPropagation();
                if (disabled) return;
                change(editing.index, editing.model);
                setEditing(null);
              }}
            >
              <fieldset className="providers-fieldset" disabled={disabled}>
                <code className="providers-dialog-model-id">{editing.model.model_id}</code>
                <div className="form-group">
                  <label htmlFor={id + "-name"}>{t("providers.displayName")}</label>
                  <input
                    id={id + "-name"}
                    className="input-control"
                    value={editing.model.display_name}
                    onChange={(e) =>
                      setEditing({
                        ...editing,
                        model: { ...editing.model, display_name: e.target.value },
                      })
                    }
                  />
                </div>
                {endpoints && (
                  <div className="form-group">
                    <label htmlFor={id + "-endpoint"}>{t("providers.endpoint")}</label>
                    <select
                      id={id + "-endpoint"}
                      className="input-control"
                      value={editing.model.endpoint}
                      onChange={(e) => {
                        const endpoint = endpoints.find((entry) => entry.key === e.target.value);
                        if (endpoint)
                          setEditing({
                            ...editing,
                            model: {
                              ...editing.model,
                              endpoint: endpoint.key,
                              capability: endpoint.capability,
                              facts:
                                endpoint.capability === editing.model.capability
                                  ? editing.model.facts
                                  : emptyFacts(),
                            },
                          });
                      }}
                    >
                      {endpoints.map((endpoint) => (
                        <option key={endpoint.key} value={endpoint.key}>
                          {t("providers.endpointOption", {
                            endpoint: endpoint.key,
                            capability: t(`providers.capability.${endpoint.capability}`),
                          })}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
                {editing.model.capability !== "tts" && (
                  <FactsEditor
                    key={editing.model.capability}
                    model={editing.model}
                    invalid={error?.details?.field === "facts"}
                    onChange={(facts) =>
                      setEditing({ ...editing, model: { ...editing.model, facts } })
                    }
                  />
                )}
                {editing.model.user_added && (
                  <Button
                    type="button"
                    variant="ghost"
                    className="providers-danger"
                    disabled={disabled}
                    onClick={() => {
                      onChange(models.filter((_, index) => index !== editing.index));
                      setEditing(null);
                    }}
                  >
                    <Trash2 size={14} />
                    {t("providers.removeModelRow", { row: editing.index + 1 })}
                  </Button>
                )}
              </fieldset>
            </form>
          </Dialog>,
          document.body
        )}
      {adding &&
        createPortal(
          <Dialog
            title={t("providers.addModel")}
            onClose={() => setAdding(false)}
            dirty={!!modelId}
            footer={(requestClose) => (
              <>
                <Button type="button" onClick={requestClose}>
                  {t("providers.cancel")}
                </Button>
                <Button
                  type="submit"
                  form={id + "-add"}
                  variant="primary"
                  disabled={disabled || !modelId.trim() || !selected}
                >
                  {t("providers.addModel")}
                </Button>
              </>
            )}
          >
            <form
              id={id + "-add"}
              className="providers-form"
              onSubmit={(e) => {
                e.preventDefault();
                e.stopPropagation();
                if (disabled || !modelId.trim() || !selected) return;
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
                      (model) =>
                        model.capability === capability && model.enabled && model.is_default
                    ),
                  },
                ]);
                setModelId("");
                setAdding(false);
                setDuplicate(false);
              }}
            >
              <fieldset className="providers-fieldset" disabled={disabled}>
                <div className="form-group">
                  <label htmlFor={id + "-new"}>{t("providers.modelId")}</label>
                  <input
                    id={id + "-new"}
                    className="input-control"
                    value={modelId}
                    required
                    onChange={(e) => {
                      setModelId(e.target.value);
                      setDuplicate(false);
                    }}
                    aria-invalid={duplicate}
                  />
                </div>
                <div className="form-group">
                  <label htmlFor={id + "-selection"}>
                    {t(endpoints ? "providers.endpoint" : "providers.capability")}
                  </label>
                  <select
                    id={id + "-selection"}
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
                      : [...new Set(capabilities)].map((capability) => (
                          <option key={capability} value={capability}>
                            {t(`providers.capability.${capability}`)}
                          </option>
                        ))}
                  </select>
                </div>
                {duplicate && (
                  <p className="providers-danger" role="alert">
                    {t("providers.duplicateModel")}
                  </p>
                )}
              </fieldset>
            </form>
          </Dialog>,
          document.body
        )}
    </fieldset>
  );
}
