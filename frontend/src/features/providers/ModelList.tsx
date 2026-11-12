import {
  useCallback,
  useContext,
  useDeferredValue,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { ChevronDown, ChevronRight, Plus, Search, Trash2 } from "lucide-react";
import type { CustomEndpoint, ProviderCapability, ProviderModel } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Dialog } from "../../components/ui/Dialog";
import { useT } from "../../lib/i18n";
import type { ActionError } from "../../lib/rtClient";
import { emptyFacts, newModel, parseDurations, parseResolutions } from "./customModels";
import { enabledCounts, filterModels } from "./modelPresentation";
import { ModelRow } from "./ModelRow";
import { CapabilityIcon } from "./CapabilityIcon";
import { isGroupOpen, ModelGroupsContext, type ModelGroups } from "./modelGroups";

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
  notice,
}: {
  models: ProviderModel[];
  onChange: (models: ProviderModel[]) => void;
  capabilities: ProviderCapability[];
  endpoints?: CustomEndpoint[];
  error?: ActionError | null;
  disabled?: boolean;
  toolbar?: ReactNode;
  /** Explanation shown on its own row under the toolbar (never inside it: long text would crowd the controls). */
  notice?: string;
}) {
  const t = useT();
  const id = useId();
  const [search, setSearch] = useState("");
  const groupState = useContext(ModelGroupsContext);
  const [localExpanded, setLocalExpanded] = useState<ModelGroups>({});
  const expanded = groupState?.expanded ?? localExpanded;
  const toggleGroup = (capability: ProviderCapability) => {
    if (groupState) groupState.toggle(capability);
    else setLocalExpanded((previous) => ({ ...previous, [capability]: !previous[capability] }));
  };
  // While searching, every group with a match is shown so results are never hidden behind a chevron.
  const searching = search.trim() !== "";
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<{ index: number; model: ProviderModel } | null>(null);
  const [modelId, setModelId] = useState("");
  const [selection, setSelection] = useState("");
  const [duplicate, setDuplicate] = useState(false);
  const selected = endpoints
    ? (endpoints.find((entry) => entry.key === selection)?.key ?? endpoints[0]?.key ?? "")
    : (capabilities.find((value) => value === selection) ?? capabilities[0] ?? "");
  // Typing stays responsive: the (possibly long) filtered list renders at a lower priority.
  const deferredSearch = useDeferredValue(search);
  const filtered = useMemo(() => filterModels(models, deferredSearch), [models, deferredSearch]);
  const counts = useMemo(() => enabledCounts(filtered), [filtered]);
  const indexOf = useMemo(() => new Map(models.map((model, i) => [model, i])), [models]);
  const groups = useMemo(() => {
    const byCapability = new Map<ProviderCapability, ProviderModel[]>();
    for (const model of filtered) {
      const rows = byCapability.get(model.capability);
      if (rows) rows.push(model);
      else byCapability.set(model.capability, [model]);
    }
    return [...byCapability];
  }, [filtered]);
  // Stable handlers (read the latest data through refs) so memoised rows only re-render with their data.
  const modelsRef = useRef(models);
  const onChangeRef = useRef(onChange);
  useLayoutEffect(() => {
    modelsRef.current = models;
    onChangeRef.current = onChange;
  });
  const change = useCallback((index: number, patch: Partial<ProviderModel>) => {
    const models = modelsRef.current;
    const updated = { ...models[index], ...patch };
    if (!updated.enabled) updated.is_default = false;
    if (updated.capability !== models[index].capability) {
      if (!patch.facts) updated.facts = emptyFacts();
      updated.is_default = false;
    }
    onChangeRef.current(
      models.map((model, i) =>
        i === index
          ? updated
          : updated.is_default && model.capability === updated.capability
            ? { ...model, is_default: false }
            : model
      )
    );
  }, []);
  const edit = useCallback((index: number) => {
    const model = modelsRef.current[index];
    setEditing({ index, model: { ...model, facts: { ...model.facts } } });
  }, []);
  const bulk = (enabled: boolean) => {
    const targets = new Set(filtered);
    onChange(
      models.map((model) =>
        targets.has(model) ? { ...model, enabled, is_default: enabled && model.is_default } : model
      )
    );
  };
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
      {notice && <p className="providers-model-notice">{notice}</p>}
      {(error?.details?.field === "models" || error?.details?.field === "facts") && (
        <p className="providers-danger" role="alert">
          {t(error.details.field === "facts" ? "providers.factsError" : "providers.modelsError")}
        </p>
      )}
      <div className="providers-models">
        {groups.map(([capability, rows]) => {
          const open = isGroupOpen(expanded, capability, searching);
          return (
            <section className="providers-model-group" key={capability}>
              <button
                type="button"
                className="providers-model-group-heading"
                aria-expanded={open}
                onClick={() => toggleGroup(capability)}
              >
                <CapabilityIcon value={capability} />
                <strong>{t(`providers.capability.${capability}`)}</strong>
                <span className="providers-meta">
                  {t("providers.enabledCount", enabledCounts(rows))}
                </span>
                {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              </button>
              {open &&
                rows.map((model) => (
                  <ModelRow
                    key={`${model.model_id}-${capability}`}
                    model={model}
                    index={indexOf.get(model) ?? 0}
                    disabled={disabled}
                    showEndpoint={!!endpoints}
                    onChange={change}
                    onEdit={edit}
                  />
                ))}
            </section>
          );
        })}
        {!filtered.length && !(notice && !models.length) && (
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
