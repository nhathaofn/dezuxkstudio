import React, { useState } from "react";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Copy,
  FolderOpen,
  KeyRound,
  LogIn,
  LogOut,
  Plug,
  PlugZap,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  XCircle,
} from "lucide-react";
import type {
  AgentAuthState,
  AgentConnectionState,
  AgentProviderInfo,
} from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { getBridge, isDesktop } from "../../lib/bridge";
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";
import { useFlash } from "../../lib/hooks/useFlash";
import { COPY_FEEDBACK_MS, useAgentStore } from "./useAgentStore";

function statusMeta(state: AgentConnectionState, t: (k: MessageKey) => string) {
  switch (state) {
    case "connected":
      return {
        color: "var(--status-connected)",
        icon: <CheckCircle2 size={13} style={{ color: "var(--status-connected)" }} />,
        label: t("agent.status.connected"),
      };
    case "connecting":
      return {
        color: "var(--status-connecting)",
        icon: (
          <RefreshCw
            size={13}
            className="animate-spin"
            style={{ color: "var(--status-connecting)" }}
          />
        ),
        label: t("agent.status.connecting"),
      };
    case "unauthenticated":
      return {
        color: "var(--status-connecting)",
        icon: <AlertTriangle size={13} style={{ color: "var(--status-connecting)" }} />,
        label: t("agent.status.unauthenticated"),
      };
    case "missing":
      return {
        color: "var(--status-connecting)",
        icon: <Search size={13} style={{ color: "var(--status-connecting)" }} />,
        label: t("agent.status.missing"),
      };
    case "error":
      return {
        color: "var(--status-disconnected)",
        icon: <XCircle size={13} style={{ color: "var(--status-disconnected)" }} />,
        label: t("agent.status.error"),
      };
    case "disconnected":
    default:
      return {
        color: "var(--text-muted)",
        icon: <Plug size={13} style={{ color: "var(--text-muted)" }} />,
        label: t("agent.status.disconnected"),
      };
  }
}

function authLabel(
  auth: AgentAuthState,
  accountInfo: string,
  t: (k: MessageKey) => string
): string {
  switch (auth) {
    case "authenticated":
      return accountInfo || t("agent.connections.authAuthenticated");
    case "unauthenticated":
      return t("agent.connections.authUnauthenticated");
    case "unsupported":
      return t("agent.connections.authUnsupported");
    case "unknown":
    default:
      return t("agent.connections.authUnknown");
  }
}

export interface AgentProviderCardProps {
  provider: AgentProviderInfo;
  disabled?: boolean;
}

interface ModelPickerProps {
  provider: AgentProviderInfo;
  disabled: boolean;
  onChange: (model: string, effort: string) => void;
}

// ModelPicker lists exactly the models the provider's CLI reports (never a hardcoded list).
const ModelPicker: React.FC<ModelPickerProps> = ({ provider, disabled, onChange }) => {
  const t = useT();
  const models = provider.models ?? [];
  const selected = models.find((m) => m.id === provider.selected_model);
  const efforts = selected?.efforts ?? [];
  const currentName = models.find((m) => m.id === provider.current_model)?.name;

  if (models.length === 0) {
    return (
      <div className="agent-model-row">
        <span className="agent-provider-meta__label">{t("agent.model.label")}:</span>
        <span className="agent-model-row__hint">
          {provider.id === "antigravity"
            ? t("agent.model.connectToLoad")
            : t("agent.model.unavailable")}
        </span>
      </div>
    );
  }

  return (
    <div className="agent-model-row">
      <label className="agent-provider-meta__label" htmlFor={`agent-model-${provider.id}`}>
        {t("agent.model.label")}:
      </label>
      <select
        id={`agent-model-${provider.id}`}
        className="input-control agent-model-row__select"
        value={provider.selected_model}
        onChange={(e) => onChange(e.target.value, "")}
        disabled={disabled}
        title={selected?.description || ""}
      >
        <option value="">{t("agent.model.providerDefault")}</option>
        {models.map((m) => (
          <option key={m.id} value={m.id} title={m.description || m.id}>
            {m.name}
          </option>
        ))}
      </select>
      {efforts.length > 0 && (
        <select
          aria-label={t("agent.model.effort")}
          className="input-control agent-model-row__select agent-model-row__select--effort"
          value={provider.selected_effort}
          onChange={(e) => onChange(provider.selected_model, e.target.value)}
          disabled={disabled}
        >
          <option value="">
            {t("agent.model.effortDefault")}
            {selected?.default_effort ? ` (${selected.default_effort})` : ""}
          </option>
          {efforts.map((e) => (
            <option key={e} value={e}>
              {e}
            </option>
          ))}
        </select>
      )}
      {provider.active && currentName && (
        <span className="agent-model-row__hint">
          {t("agent.model.running")}: {currentName}
        </span>
      )}
    </div>
  );
};

export const AgentProviderCard: React.FC<AgentProviderCardProps> = ({
  provider,
  disabled = false,
}) => {
  const t = useT();
  const probeProvider = useAgentStore((s) => s.probeProvider);
  const setCustomPath = useAgentStore((s) => s.setCustomPath);
  const setModel = useAgentStore((s) => s.setModel);
  const loginProvider = useAgentStore((s) => s.loginProvider);
  const logoutProvider = useAgentStore((s) => s.logoutProvider);
  const connectProvider = useAgentStore((s) => s.connectProvider);
  const disconnect = useAgentStore((s) => s.disconnect);
  const busyAction = useAgentStore((s) => s.busyAction);

  const [probing, setProbing] = useState(false);
  const [customDraft, setCustomDraft] = useState<string | null>(null);
  const [copiedHint, flashCopiedHint] = useFlash(COPY_FEEDBACK_MS);
  const [showKeyForm, setShowKeyForm] = useState(false);
  const [apiKeyDraft, setApiKeyDraft] = useState("");

  const customInput = customDraft ?? (provider.custom_path || "");
  const isDirty = customDraft !== null && customDraft !== (provider.custom_path || "");
  const badge = statusMeta(provider.state, t);
  const canConnect =
    provider.supported && provider.state !== "missing" && Boolean(provider.resolved_path);

  const handleProbe = async () => {
    setProbing(true);
    try {
      await probeProvider(provider.id);
    } finally {
      setProbing(false);
    }
  };

  const handleSavePath = async () => {
    setProbing(true);
    try {
      await setCustomPath(provider.id, customInput.trim());
      setCustomDraft(null);
    } finally {
      setProbing(false);
    }
  };

  const handleResetPath = async () => {
    setCustomDraft(null);
    setProbing(true);
    try {
      await setCustomPath(provider.id, "");
    } finally {
      setProbing(false);
    }
  };

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseExecutable) return;
    try {
      const selected = await bridge.BrowseExecutable(
        t("agent.connections.browseTitle", { name: provider.name })
      );
      if (selected) {
        setCustomDraft(selected);
      }
    } catch (err) {
      console.error("Browse agent executable failed:", err);
    }
  };

  const handleCopyHint = async () => {
    try {
      await navigator.clipboard.writeText(provider.install_hint);
      flashCopiedHint(true);
    } catch {
      // Ignore clipboard failures outside secure context
    }
  };

  const handleKeyLogin = async () => {
    await loginProvider(provider.id, apiKeyDraft.trim());
    setApiKeyDraft("");
    setShowKeyForm(false);
  };

  return (
    <div className={`agent-provider-card${provider.active ? " agent-provider-card--active" : ""}`}>
      <div className="agent-provider-card__top">
        <div className="agent-provider-card__identity">
          <div className="agent-provider-card__title-row">
            <span className="agent-provider-card__name">{provider.name}</span>
            <Badge>{provider.executable}</Badge>
          </div>
          <p className="agent-provider-card__desc">{provider.description}</p>
        </div>

        <span className="agent-provider-card__status">
          {badge.icon}
          <span style={{ color: badge.color }}>
            {provider.supported ? badge.label : t("agent.connections.authUnsupported")}
          </span>
        </span>
      </div>

      {!provider.supported && provider.unsupported_reason && (
        <div className="agent-provider-note">{provider.unsupported_reason}</div>
      )}

      <div className="agent-provider-meta">
        <span className="agent-provider-meta__label">{t("agent.connections.version")}:</span>
        <span className="agent-provider-meta__val">{provider.version || "—"}</span>

        <span className="agent-provider-meta__label">{t("agent.connections.auth")}:</span>
        <span className="agent-provider-meta__val" title={provider.account_info || ""}>
          {authLabel(provider.auth_state, provider.account_info, t)}
        </span>

        <span className="agent-provider-meta__label">{t("agent.connections.location")}:</span>
        <span
          className="agent-provider-meta__val"
          title={provider.resolved_path || t("agent.source.none")}
        >
          {provider.resolved_path || t("agent.source.none")}
        </span>

        {provider.isolated_profile_dir && (
          <>
            <span className="agent-provider-meta__label">{t("agent.connections.profileDir")}:</span>
            <span className="agent-provider-meta__val" title={provider.isolated_profile_dir}>
              {provider.isolated_profile_dir}
            </span>
          </>
        )}

        {provider.protocol_version > 0 && (
          <>
            <span className="agent-provider-meta__label">{t("agent.connections.protocol")}:</span>
            <span className="agent-provider-meta__val">
              ACP v{provider.protocol_version}
              {provider.agent_name ? ` (${provider.agent_name})` : ""}
            </span>
          </>
        )}
      </div>

      {provider.supported && provider.state !== "missing" && (
        <ModelPicker
          provider={provider}
          disabled={disabled || probing || busyAction || provider.busy}
          onChange={(model, effort) => void setModel(provider.id, model, effort)}
        />
      )}

      {provider.install_hint && provider.supported && (
        <div className="agent-hint-row">
          <span className="agent-hint-row__label">{t("agent.connections.installHint")}:</span>
          <code className="agent-hint-row__code">{provider.install_hint}</code>
          <Button
            size="sm"
            iconOnly
            onClick={() => void handleCopyHint()}
            title={t("agent.connections.copyCommand")}
            aria-label={t("agent.connections.copyCommand")}
          >
            {copiedHint ? <Check size={12} /> : <Copy size={12} />}
          </Button>
        </div>
      )}

      {provider.last_error && (
        <div className="agent-provider-error" role="alert">
          {translateServerMessage(t, provider.last_error_key, provider.last_error_params) ??
            provider.last_error}
        </div>
      )}

      {showKeyForm && (
        <div className="agent-path-row">
          <input
            type="password"
            className="binary-path-input"
            value={apiKeyDraft}
            onChange={(e) => setApiKeyDraft(e.target.value)}
            placeholder={
              provider.id === "antigravity"
                ? t("agent.connections.geminiApiKeyPlaceholder")
                : t("agent.connections.apiKeyPlaceholder")
            }
            disabled={disabled || probing || busyAction}
          />
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleKeyLogin()}
            disabled={disabled || probing || busyAction || !apiKeyDraft.trim()}
          >
            {t("agent.connections.apiKeySubmit")}
          </Button>
        </div>
      )}

      <div className="agent-path-row">
        <input
          type="text"
          className="binary-path-input"
          value={customInput}
          onChange={(e) => setCustomDraft(e.target.value)}
          placeholder={t("agent.connections.pathPlaceholder")}
          disabled={disabled || probing || busyAction}
        />
        {isDesktop() && (
          <Button
            size="sm"
            onClick={() => void handleBrowse()}
            disabled={disabled || probing || busyAction}
            title={t("agent.connections.browse")}
          >
            <FolderOpen size={12} />
            {t("agent.connections.browse")}
          </Button>
        )}
        {isDirty && (
          <Button
            variant="primary"
            size="sm"
            onClick={() => void handleSavePath()}
            disabled={disabled || probing || busyAction}
          >
            <Save size={12} />
            {t("agent.connections.savePath")}
          </Button>
        )}
        {Boolean(provider.custom_path) && !isDirty && (
          <Button
            size="sm"
            onClick={() => void handleResetPath()}
            disabled={disabled || probing || busyAction}
          >
            <RotateCcw size={12} />
            {t("agent.connections.clearPath")}
          </Button>
        )}
      </div>

      <div className="agent-provider-card__actions">
        <Button
          size="sm"
          onClick={() => void handleProbe()}
          disabled={disabled || probing || busyAction}
        >
          <RefreshCw size={12} className={probing ? "animate-spin" : ""} />
          {t("agent.connections.probe")}
        </Button>

        {provider.supported && provider.resolved_path && (
          <>
            {provider.auth_state === "authenticated" ? (
              <Button
                size="sm"
                onClick={() => void logoutProvider(provider.id)}
                disabled={disabled || busyAction}
              >
                <LogOut size={12} />
                {t("agent.connections.signOut")}
              </Button>
            ) : (
              <>
                <Button
                  size="sm"
                  onClick={() => setShowKeyForm((v) => !v)}
                  disabled={disabled || busyAction}
                >
                  <KeyRound size={12} />
                  {t("agent.connections.apiKeyToggle")}
                </Button>
                <Button
                  size="sm"
                  onClick={() => void loginProvider(provider.id)}
                  disabled={disabled || busyAction}
                >
                  <LogIn size={12} />
                  {t("agent.connections.signIn")}
                </Button>
              </>
            )}
          </>
        )}

        {provider.active ? (
          <Button size="sm" onClick={() => void disconnect()} disabled={disabled || busyAction}>
            <Plug size={12} />
            {t("agent.connections.disconnect")}
          </Button>
        ) : (
          <Button
            variant="primary"
            size="sm"
            onClick={() => void connectProvider(provider.id)}
            disabled={disabled || !canConnect || busyAction || provider.state === "connecting"}
          >
            <PlugZap size={12} />
            {t("agent.connections.connect")}
          </Button>
        )}
      </div>
    </div>
  );
};
