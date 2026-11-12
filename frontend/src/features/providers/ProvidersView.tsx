import { useEffect, useRef, useState } from "react";
import {
  Check,
  ExternalLink,
  KeyRound,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Server,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import {
  ProviderCapabilityValues,
  type BuiltinProvider,
  type CustomProvider,
  type ProviderCapability,
  type ProviderCredential,
} from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { LoadingSkeleton } from "../../components/ui/LoadingSkeleton";
import { appConfig } from "../../config/appConfig";
import { openExternal } from "../../lib/bridge";
import { useFlash } from "../../lib/hooks/useFlash";
import { useT } from "../../lib/i18n";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { useConnectionStore } from "../connection/useConnectionStore";
import { CredentialForm } from "./CredentialForm";
import { CustomProviderForm } from "./CustomProviderForm";
import { fieldLabel } from "./fieldLabel";
import { useProvidersStore } from "./useProvidersStore";

function Capabilities({ values }: { values: ProviderCapability[] }) {
  const t = useT();
  return (
    <div className="chips-row">
      {values.map((capability) => (
        <Badge key={capability}>{t(`providers.capability.${capability}`)}</Badge>
      ))}
    </div>
  );
}

function TestResult({ id }: { id: string }) {
  const t = useT();
  const result = useProvidersStore((s) => s.testResults[id]);
  if (!result) return null;
  return (
    <div
      className={`providers-notice ${result.ok ? "providers-notice--success" : "providers-notice--danger"}`}
      role="status"
    >
      <span>{t(`providers.outcome.${result.outcome}`)}</span>
      {result.ok && <span>{t("providers.latency", { ms: result.latency_ms })}</span>}
      {result.status > 0 && <span>{t("providers.httpStatus", { status: result.status })}</span>}
      {result.detail && <p>{result.detail}</p>}
      {result.ok && result.models.length > 0 && (
        <details>
          <summary>
            {t("providers.testModels", {
              count: Math.min(result.models.length, appConfig.ui.providerTestModelLimit),
            })}
          </summary>
          <ul>
            {result.models.slice(0, appConfig.ui.providerTestModelLimit).map((model) => (
              <li key={model}>
                <code>{model}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function ProviderActions({
  id,
  disabled,
  active,
  onActivate,
  onTest,
  onEdit,
  onDelete,
}: {
  id: string;
  disabled: boolean;
  active?: boolean;
  onActivate?: () => Promise<void>;
  onTest: () => Promise<void>;
  onEdit: () => void;
  onDelete: () => Promise<void>;
}) {
  const t = useT();
  const testing = useProvidersStore((s) => s.testing[id]);
  const [confirm, showConfirm, clearConfirm] = useFlash(appConfig.ui.confirmDeleteMs);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ActionError | null>(null);
  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(toActionError(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="providers-action-block">
      <div className="card-actions-row">
        {onActivate && !active && (
          <Button
            type="button"
            disabled={disabled || busy || testing}
            onClick={() => void run(onActivate)}
          >
            <Check size={13} />
            {t("providers.activate")}
          </Button>
        )}
        <Button
          type="button"
          disabled={disabled || busy || testing}
          onClick={() => void run(onTest)}
        >
          <Play size={13} />
          {t(testing ? "providers.testing" : "providers.test")}
        </Button>
        <Button type="button" disabled={disabled || busy || testing} onClick={onEdit}>
          <Pencil size={13} />
          {t("providers.edit")}
        </Button>
        <Button
          type="button"
          className="providers-danger"
          disabled={disabled || busy || testing}
          onClick={() => {
            if (!confirm) {
              showConfirm(true);
              return;
            }
            clearConfirm();
            void run(onDelete);
          }}
        >
          <Trash2 size={13} />
          {t(confirm ? "providers.confirmDelete" : "providers.delete")}
        </Button>
      </div>
      {confirm && (
        <p className="providers-danger" role="status">
          {t("providers.deleteWarning")}
        </p>
      )}
      <ErrorText error={error} />
      <TestResult id={id} />
    </div>
  );
}

function BuiltinCard({
  provider,
  disabled,
  onAdd,
  onEdit,
}: {
  provider: BuiltinProvider;
  disabled: boolean;
  onAdd: () => void;
  onEdit: (credential: ProviderCredential) => void;
}) {
  const t = useT();
  const activate = useProvidersStore((s) => s.activateCredential);
  const test = useProvidersStore((s) => s.testCredential);
  const remove = useProvidersStore((s) => s.deleteCredential);
  return (
    <Card
      icon={KeyRound}
      title={provider.display_name}
      actions={
        <Button type="button" disabled={disabled} onClick={onAdd}>
          <Plus size={13} />
          {t("providers.addCredential")}
        </Button>
      }
    >
      <div className="providers-meta">
        <Capabilities values={provider.capabilities} />
        <Badge className={provider.configured ? "providers-success" : ""}>
          {t(provider.configured ? "providers.configured" : "providers.notConfigured")}
        </Badge>
        {provider.docs_url && (
          <Button type="button" onClick={() => openExternal(provider.docs_url)}>
            <ExternalLink size={13} />
            {t("providers.docs")}
          </Button>
        )}
      </div>
      <details className="providers-details">
        <summary>{t("providers.credentialsCount", { count: provider.credentials.length })}</summary>
        {provider.credentials.length === 0 && (
          <p className="subtitle">{t("providers.noCredentials")}</p>
        )}
        {provider.credentials.map((credential) => (
          <div key={credential.id} className="providers-credential">
            <div className="providers-meta">
              <strong>{credential.name}</strong>
              {credential.active && (
                <Badge className="providers-success">{t("providers.active")}</Badge>
              )}
            </div>
            <dl className="providers-values">
              {Object.entries({ ...credential.values, ...credential.secret_hints }).map(
                ([key, value]) => (
                  <div key={key}>
                    <dt>{fieldLabel(t, key)}</dt>
                    <dd>
                      <code>{value}</code>
                    </dd>
                  </div>
                )
              )}
            </dl>
            <ProviderActions
              id={credential.id}
              disabled={disabled}
              active={credential.active}
              onActivate={() => activate(credential.id)}
              onTest={() => test(credential.id)}
              onEdit={() => onEdit(credential)}
              onDelete={() => remove(credential.id)}
            />
          </div>
        ))}
      </details>
    </Card>
  );
}

function CustomCard({
  provider,
  disabled,
  onEdit,
}: {
  provider: CustomProvider;
  disabled: boolean;
  onEdit: () => void;
}) {
  const t = useT();
  const test = useProvidersStore((s) => s.testCustom);
  const remove = useProvidersStore((s) => s.deleteCustom);
  return (
    <Card icon={Server} title={provider.name}>
      <div className="providers-meta">
        <Badge>{t(`providers.protocol.${provider.discovery_format}`)}</Badge>
        <Capabilities values={provider.capabilities} />
        <Badge>{t("providers.modelsCount", { count: provider.models.length })}</Badge>
      </div>
      <dl className="providers-values">
        <div>
          <dt>{t("providers.field.base_url")}</dt>
          <dd>
            <code>{provider.base_url}</code>
          </dd>
        </div>
        <div>
          <dt>{t("providers.field.api_key")}</dt>
          <dd>
            <code>{provider.key_hint || t("providers.noKey")}</code>
          </dd>
        </div>
      </dl>
      <ProviderActions
        id={provider.id}
        disabled={disabled}
        onTest={() => test(provider.id)}
        onEdit={onEdit}
        onDelete={() => remove(provider.id)}
      />
    </Card>
  );
}

type Editor =
  | { kind: "credential"; provider: BuiltinProvider; credential?: ProviderCredential }
  | { kind: "custom"; provider?: CustomProvider };

export function ProvidersView() {
  const t = useT();
  const connected = useConnectionStore((s) => s.status === "connected");
  const data = useProvidersStore((s) => s.data);
  const loading = useProvidersStore((s) => s.loading);
  const error = useProvidersStore((s) => s.error);
  const list = useProvidersStore((s) => s.list);
  const [filter, setFilter] = useState<"all" | "custom" | ProviderCapability>("all");
  const [editor, setEditor] = useState<Editor | null>(null);
  const editorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!editor) return;
    editorRef.current?.focus();
    editorRef.current?.scrollIntoView({ block: "nearest" });
  }, [editor]);
  const [saved, showSaved] = useFlash(appConfig.ui.copyFeedbackMs);
  const onSaved = () => {
    setEditor(null);
    showSaved(true);
  };
  const builtins =
    data?.builtins.filter(
      (provider) =>
        filter !== "custom" && (filter === "all" || provider.capabilities.includes(filter))
    ) ?? [];
  const custom =
    data?.custom.filter(
      (provider) =>
        filter === "all" || filter === "custom" || provider.capabilities.includes(filter)
    ) ?? [];
  return (
    <div className="scrollable-pane">
      <div className="cards-stack providers-view">
        <Card
          icon={KeyRound}
          title={t("providers.title")}
          subtitle={t("providers.subtitle")}
          actions={
            <Button type="button" disabled={!connected || loading} onClick={() => void list()}>
              <RefreshCw size={13} className={loading ? "animate-spin" : ""} />
              {t("providers.refresh")}
            </Button>
          }
        >
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
          {data && data.secret_backend !== "keyring" && (
            <div
              role="alert"
              className={`providers-notice providers-notice--${data.secret_backend === "memory" ? "danger" : "warning"}`}
            >
              <ShieldAlert size={18} />
              <span>{t(`providers.backend.${data.secret_backend}`)}</span>
            </div>
          )}
          <div className="providers-meta">
            <div className="settings-btn-group" role="group" aria-label={t("providers.filter")}>
              {(["all", ...ProviderCapabilityValues, "custom"] as const).map((value) => (
                <Button
                  key={value}
                  type="button"
                  variant={filter === value ? "primary" : "secondary"}
                  aria-pressed={filter === value}
                  onClick={() => setFilter(value)}
                >
                  {t(`providers.capability.${value}`)}
                </Button>
              ))}
            </div>
            <Button
              type="button"
              disabled={!connected || !data}
              onClick={() => setEditor({ kind: "custom" })}
            >
              <Plus size={13} />
              {t("providers.addCustom")}
            </Button>
          </div>
        </Card>
        {editor && (
          <div ref={editorRef} tabIndex={-1}>
            {editor.kind === "credential" && (
              <CredentialForm
                key={editor.credential?.id ?? editor.provider.id}
                provider={editor.provider}
                credential={editor.credential}
                onClose={() => setEditor(null)}
                onSaved={onSaved}
                disabled={!connected}
              />
            )}
            {editor.kind === "custom" && data && (
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
        )}
        {loading && !data && <LoadingSkeleton />}
        {builtins.map((provider) => (
          <BuiltinCard
            key={provider.id}
            provider={provider}
            disabled={!connected}
            onAdd={() => setEditor({ kind: "credential", provider })}
            onEdit={(credential) => setEditor({ kind: "credential", provider, credential })}
          />
        ))}
        {custom.length > 0 && (
          <h2 className="providers-section-title">{t("providers.customTitle")}</h2>
        )}
        {custom.map((provider) => (
          <CustomCard
            key={provider.id}
            provider={provider}
            disabled={!connected}
            onEdit={() => setEditor({ kind: "custom", provider })}
          />
        ))}
        {data && !builtins.length && !custom.length && (
          <div className="empty-state">{t("providers.empty")}</div>
        )}
      </div>
    </div>
  );
}
