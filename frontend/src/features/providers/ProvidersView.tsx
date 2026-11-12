import { useEffect, useId, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Plus, RefreshCw, Search, ShieldAlert, X } from "lucide-react";
import {
  ProviderCapabilityValues,
  type BuiltinProvider,
  type CustomProvider,
  type ProviderCapability,
  type ProviderCredential,
} from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { ErrorText } from "../../components/ui/ErrorText";
import { LoadingSkeleton } from "../../components/ui/LoadingSkeleton";
import { appConfig } from "../../config/appConfig";
import { useFlash } from "../../lib/hooks/useFlash";
import { useT } from "../../lib/i18n";
import { useConnectionStore } from "../connection/useConnectionStore";
import { CredentialForm } from "./CredentialForm";
import { DefaultModels } from "./DefaultModels";
import { CustomProviderForm } from "./CustomProviderForm";
import { ProviderDetail } from "./ProviderDetail";
import { ModelGroupsContext, type ModelGroups } from "./modelGroups";
import {
  initialProvider,
  parseProviderLocation,
  providerGroups,
  providerLocation,
  providerName,
  providerStatus,
  type ProviderEntry,
} from "./layout";
import { useProvidersStore } from "./useProvidersStore";
import "./providers.css";

type Editor =
  | { kind: "credential"; provider: BuiltinProvider; credential?: ProviderCredential }
  | { kind: "custom"; provider?: CustomProvider };

export function ProvidersView() {
  const t = useT();
  const id = useId();
  const connected = useConnectionStore((s) => s.status === "connected");
  const data = useProvidersStore((s) => s.data);
  const loading = useProvidersStore((s) => s.loading);
  const error = useProvidersStore((s) => s.error);
  const list = useProvidersStore((s) => s.list);
  const [params, setParams] = useSearchParams();
  const { sub, providerId } = parseProviderLocation(params);
  const selected = initialProvider(data, providerId);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<"all" | ProviderCapability>("all");
  const [editor, setEditor] = useState<Editor | null>(null);
  const [modelGroups, setModelGroups] = useState<Record<string, ModelGroups>>({});
  const [dismissedNotice, setDismissedNotice] = useState("");
  const [saved, showSaved] = useFlash(appConfig.ui.copyFeedbackMs);
  const groups = providerGroups(data, search, filter);
  const visible = Object.values(groups).flat();
  const backendWarning = data && data.secret_backend !== "keyring" ? data.secret_backend : null;
  const noticeKey = JSON.stringify(backendWarning);
  const selectedId = selected?.provider.id ?? "";
  const listRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    // The selection can come from the URL or the mobile picker: bring its row into view.
    listRef.current
      ?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]')
      ?.scrollIntoView({ block: "nearest" });
  }, [selectedId, data]);
  useEffect(() => {
    if (!data || selectedId === providerId) return;
    setParams(providerLocation(params, { providerId: selectedId }), { replace: true });
  }, [data, selectedId, providerId, params, setParams]);
  const select = (entry: ProviderEntry) =>
    setParams(providerLocation(params, { providerId: entry.provider.id, sub: "providers" }));
  const onSaved = () => {
    setEditor(null);
    showSaved(true);
  };
  const addCustom = (
    <Button
      type="button"
      variant="ghost"
      className="providers-add-custom"
      disabled={!connected || !data}
      onClick={() => setEditor({ kind: "custom" })}
    >
      <Plus size={14} />
      {t("providers.addCustom")}
    </Button>
  );
  const groupLabels = {
    attention: "providers.attention",
    configured: "providers.configured",
    unconfigured: "providers.notConfigured",
    custom: "providers.customTitle",
  } as const;
  return (
    <ModelGroupsContext.Provider
      value={{
        collapsed: modelGroups[selectedId] ?? {},
        toggle: (capability) =>
          setModelGroups((previous) => ({
            ...previous,
            [selectedId]: {
              ...previous[selectedId],
              [capability]: !previous[selectedId]?.[capability],
            },
          })),
      }}
    >
      <div className="providers-view">
        <div className="providers-topbar">
          <div className="providers-segments" role="tablist" aria-label={t("providers.title")}>
            {(["providers", "defaults"] as const).map((value) => (
              <button
                key={value}
                id={`${id}-${value}`}
                type="button"
                role="tab"
                aria-selected={sub === value}
                aria-controls={`${id}-content`}
                onClick={() => setParams(providerLocation(params, { sub: value }))}
              >
                {t(value === "providers" ? "providers.navigation" : "providers.defaultModels")}
              </button>
            ))}
          </div>
          <Button
            type="button"
            iconOnly
            variant="ghost"
            title={t("providers.refresh")}
            aria-label={t("providers.refresh")}
            disabled={!connected || loading}
            onClick={() => void list()}
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
          </Button>
        </div>
        {!connected && (
          <p className="subtitle" role="status">
            {t("status.connecting")}
          </p>
        )}
        <ErrorText error={error} />
        {saved && (
          <p className="providers-success" role="status">
            {t("providers.saved")}
          </p>
        )}
        {backendWarning && dismissedNotice !== noticeKey && (
          <div className="providers-banner" role="alert">
            <ShieldAlert size={16} aria-hidden="true" />
            <div>
              <span title={t(`providers.backend.${backendWarning}`)}>
                {t(`providers.backend.${backendWarning}`)}
              </span>
            </div>
            <Button
              type="button"
              iconOnly
              variant="ghost"
              title={t("providers.dismissNotice")}
              aria-label={t("providers.dismissNotice")}
              onClick={() => setDismissedNotice(noticeKey)}
            >
              <X size={14} />
            </Button>
          </div>
        )}
        {loading && !data && <LoadingSkeleton />}
        <div
          id={`${id}-content`}
          role="tabpanel"
          aria-labelledby={`${id}-${sub}`}
          className={sub === "defaults" ? "providers-defaults-pane" : "providers-panes"}
        >
          {sub === "defaults" ? (
            <DefaultModels
              disabled={!connected}
              onConfigure={() => setParams(providerLocation(params, { sub: "providers" }))}
            />
          ) : (
            <>
              <aside className="providers-sidebar" aria-label={t("providers.navigation")}>
                <label className="providers-search">
                  <Search size={14} aria-hidden="true" />
                  <input
                    className="input-control"
                    type="search"
                    value={search}
                    placeholder={t("providers.search")}
                    aria-label={t("providers.search")}
                    onChange={(event) => setSearch(event.target.value)}
                  />
                </label>
                <div className="providers-filters" role="group" aria-label={t("providers.filter")}>
                  {(["all", ...ProviderCapabilityValues] as const).map((value) => (
                    <Button
                      key={value}
                      type="button"
                      variant="ghost"
                      title={t(`providers.capability.${value}`)}
                      aria-label={t(`providers.capability.${value}`)}
                      aria-pressed={filter === value}
                      onClick={() => setFilter(value)}
                    >
                      {t(`providers.capability.${value}`)}
                    </Button>
                  ))}
                </div>
                <div
                  ref={listRef}
                  className="providers-list"
                  role="listbox"
                  aria-label={t("providers.navigation")}
                  onKeyDown={(event) => {
                    if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
                    event.preventDefault();
                    const options = Array.from(
                      event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="option"]')
                    );
                    const index = options.indexOf(document.activeElement as HTMLButtonElement);
                    const next =
                      event.key === "Home"
                        ? 0
                        : event.key === "End"
                          ? options.length - 1
                          : Math.max(
                              0,
                              Math.min(
                                options.length - 1,
                                index + (event.key === "ArrowDown" ? 1 : -1)
                              )
                            );
                    options[next]?.focus();
                  }}
                >
                  {Object.entries(groups).map(([group, entries]) => (
                    <div
                      role="group"
                      aria-label={t(groupLabels[group as keyof typeof groups])}
                      key={group}
                    >
                      <h3>
                        {t(groupLabels[group as keyof typeof groups])}{" "}
                        <span>({entries.length})</span>
                      </h3>
                      {entries.map((entry) => (
                        <button
                          type="button"
                          role="option"
                          aria-selected={selectedId === entry.provider.id}
                          tabIndex={
                            selectedId === entry.provider.id ||
                            (!visible.some((row) => row.provider.id === selectedId) &&
                              entry === visible[0])
                              ? 0
                              : -1
                          }
                          key={entry.provider.id}
                          className="providers-list-row"
                          onClick={() => select(entry)}
                          title={`${providerName(entry)} · ${t(`providers.status.${providerStatus(entry)}`)}`}
                        >
                          <span className="providers-avatar">
                            {providerName(entry).charAt(0).toLocaleUpperCase()}
                          </span>
                          <span className="providers-truncate">{providerName(entry)}</span>
                          <span
                            className={`providers-dot providers-dot--${providerStatus(entry)}`}
                            aria-label={t(`providers.status.${providerStatus(entry)}`)}
                          />
                        </button>
                      ))}
                      {group === "custom" && addCustom}
                    </div>
                  ))}
                  {!visible.length && <p className="subtitle">{t("providers.empty")}</p>}
                </div>
              </aside>
              <div className="providers-mobile-picker">
                <label htmlFor={`${id}-picker`}>{t("providers.navigation")}</label>
                <select
                  id={`${id}-picker`}
                  className="input-control"
                  value={selectedId}
                  onChange={(event) => {
                    const entry = initialProvider(data, event.target.value);
                    if (entry) select(entry);
                  }}
                >
                  {!selected && <option value="">{t("providers.selectProvider")}</option>}
                  {Object.entries(providerGroups(data)).map(([group, entries]) => (
                    <optgroup key={group} label={t(groupLabels[group as keyof typeof groups])}>
                      {entries.map((entry) => (
                        <option key={entry.provider.id} value={entry.provider.id}>
                          {providerName(entry)}
                        </option>
                      ))}
                    </optgroup>
                  ))}
                </select>
                {addCustom}
              </div>
              <div className="providers-detail">
                {selected ? (
                  <ProviderDetail
                    key={selectedId}
                    entry={selected}
                    disabled={!connected}
                    endpoints={data?.endpoints ?? []}
                    onAdd={() => {
                      if (selected.kind === "builtin")
                        setEditor({ kind: "credential", provider: selected.provider });
                    }}
                    onEditCredential={(credential) => {
                      if (selected.kind === "builtin")
                        setEditor({ kind: "credential", provider: selected.provider, credential });
                    }}
                    onEditCustom={() => {
                      if (selected.kind === "custom")
                        setEditor({ kind: "custom", provider: selected.provider });
                    }}
                    onSaved={() => showSaved(true)}
                  />
                ) : (
                  <div className="providers-getting-started">
                    <h2>{t("providers.getStarted")}</h2>
                    <p>{t("providers.getStartedHint")}</p>
                    <div className="card-actions-row">
                      {data?.builtins.slice(0, 4).map((provider) => (
                        <Button
                          key={provider.id}
                          type="button"
                          onClick={() =>
                            setParams(providerLocation(params, { providerId: provider.id }))
                          }
                        >
                          {provider.display_name}
                        </Button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </>
          )}
        </div>
        {editor?.kind === "credential" && (
          <CredentialForm
            key={editor.credential?.id ?? editor.provider.id}
            provider={editor.provider}
            credential={editor.credential}
            onClose={() => setEditor(null)}
            onSaved={onSaved}
            disabled={!connected}
          />
        )}
        {editor?.kind === "custom" && data && (
          <CustomProviderForm
            key={editor.provider?.id ?? "new"}
            provider={editor.provider}
            endpoints={data.endpoints}
            onClose={() => setEditor(null)}
            onSaved={onSaved}
            disabled={!connected}
          />
        )}
      </div>
    </ModelGroupsContext.Provider>
  );
}
