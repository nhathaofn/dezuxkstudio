import React from "react";
import { Check, Send, ShieldAlert, Sparkles, Square, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import type { AgentApprovalRequest, AgentProviderInfo, SkillInfo } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentStore } from "./useAgentStore";

/** Compact model / reasoning switcher for the active provider, next to the send button. */
const ComposerModelPicker: React.FC<{ provider: AgentProviderInfo; disabled: boolean }> = ({
  provider,
  disabled,
}) => {
  const t = useT();
  const setModel = useAgentStore((s) => s.setModel);
  const models = provider.models ?? [];
  if (models.length === 0) return null;
  const selected = models.find((m) => m.id === provider.selected_model);
  const efforts = selected?.efforts ?? [];
  return (
    <div className="agent-composer__model">
      <select
        className="agent-composer__select"
        value={provider.selected_model}
        onChange={(e) => void setModel(provider.id, e.target.value, "")}
        disabled={disabled}
        aria-label={t("agent.model.label")}
        title={selected?.description || t("agent.model.label")}
      >
        <option value="">{t("agent.model.providerDefault")}</option>
        {models.map((m) => (
          <option key={m.id} value={m.id}>
            {m.name}
          </option>
        ))}
      </select>
      {efforts.length > 0 && (
        <select
          className="agent-composer__select"
          value={provider.selected_effort}
          onChange={(e) => void setModel(provider.id, provider.selected_model, e.target.value)}
          disabled={disabled}
          aria-label={t("agent.model.effort")}
          title={t("agent.model.effort")}
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
    </div>
  );
};

export interface AgentComposerProps {
  connectedToCore: boolean;
  activeProvider: AgentProviderInfo | undefined;
  convOwner: AgentProviderInfo | undefined;
  wrongProvider: boolean;
  wrongProviderDismissed: boolean;
  convOwnerName: string;
  approvals: AgentApprovalRequest[];
  prompt: string;
  sending: boolean;
  busyAction: boolean;
  canSend: boolean;
  slashSkills: SkillInfo[];
  activeSlashIdx: number;
  inputRef: React.Ref<HTMLTextAreaElement>;
  onDismissWrongProvider: () => void;
  onSwitchProvider: (providerId: AgentProviderInfo["id"]) => void;
  onNewChat: () => void;
  onResolveApproval: (id: string, approved: boolean) => void;
  onPromptChange: (val: string) => void;
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  onInsertSlashSkill: (name: string) => void;
  onSend: () => void;
  onCancel: () => void;
}

export const AgentComposer: React.FC<AgentComposerProps> = ({
  connectedToCore,
  activeProvider,
  convOwner,
  wrongProvider,
  wrongProviderDismissed,
  convOwnerName,
  approvals,
  prompt,
  sending,
  busyAction,
  canSend,
  slashSkills,
  activeSlashIdx,
  inputRef,
  onDismissWrongProvider,
  onSwitchProvider,
  onNewChat,
  onResolveApproval,
  onPromptChange,
  onKeyDown,
  onInsertSlashSkill,
  onSend,
  onCancel,
}) => {
  const t = useT();
  return (
    <>
      {approvals.length > 0 && (
        <div className="agent-approvals">
          {approvals.map((ap) => (
            <div key={ap.id} className="agent-approval-card" role="region" aria-label={ap.title}>
              <div className="agent-approval-card__head">
                <ShieldAlert size={14} />
                <span>{ap.title || t("agent.chat.approvalTitle")}</span>
              </div>
              <p className="agent-approval-card__summary">{ap.summary}</p>
              <div className="agent-approval-card__actions">
                <Button size="sm" onClick={() => onResolveApproval(ap.id, false)}>
                  <X size={12} />
                  {t("agent.chat.reject")}
                </Button>
                <Button variant="primary" size="sm" onClick={() => onResolveApproval(ap.id, true)}>
                  <Check size={12} />
                  {t("agent.chat.approve")}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {wrongProvider && activeProvider && !wrongProviderDismissed && (
        <div className="agent-chat-banner" role="status">
          <div className="agent-chat-banner__top">
            <p className="agent-chat-banner__text">
              {t("agent.chat.wrongProviderBanner", {
                provider: convOwnerName,
                active: activeProvider.name,
              })}
            </p>
            <button
              type="button"
              className="agent-chat-banner__dismiss"
              onClick={onDismissWrongProvider}
              title={t("dialog.close")}
              aria-label={t("dialog.close")}
            >
              <X size={12} />
            </button>
          </div>
          <div className="agent-chat-banner__actions">
            {convOwner && (
              <Button
                size="sm"
                disabled={busyAction}
                onClick={() => onSwitchProvider(convOwner.id)}
              >
                {t("agent.chat.switchProvider", { provider: convOwner.name })}
              </Button>
            )}
            <Button variant="primary" size="sm" onClick={onNewChat}>
              {t("agent.newChat")}
            </Button>
          </div>
        </div>
      )}

      <div className="agent-composer">
        {slashSkills.length > 0 && (
          <div className="agent-slash-menu" role="listbox" aria-label={t("agent.slash.header")}>
            <div className="agent-slash-menu__head">
              <span>{t("agent.slash.header")}</span>
              <span className="agent-slash-menu__hint">{t("agent.slash.hint")}</span>
            </div>
            <div className="agent-slash-menu__list">
              {slashSkills.map((sk, i) => (
                <button
                  key={sk.name}
                  type="button"
                  role="option"
                  aria-selected={i === activeSlashIdx}
                  className={`agent-slash-menu__item${i === activeSlashIdx ? " is-active" : ""}`}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    onInsertSlashSkill(sk.name);
                  }}
                >
                  <Sparkles size={12} className="agent-slash-menu__icon" />
                  <span className="agent-slash-menu__name">/{sk.name}</span>
                  <span className="agent-slash-menu__desc">{sk.description}</span>
                </button>
              ))}
            </div>
          </div>
        )}
        <div
          className={`agent-composer__box${!activeProvider || wrongProvider ? " is-disabled" : ""}`}
        >
          <textarea
            ref={inputRef}
            className="agent-composer__input"
            rows={1}
            value={prompt}
            onChange={(e) => onPromptChange(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={
              !activeProvider
                ? t("agent.chat.placeholderOffline")
                : wrongProvider
                  ? t("agent.chat.wrongProviderPlaceholder", { provider: convOwnerName })
                  : t("agent.chat.placeholder", { name: activeProvider.name })
            }
            disabled={!connectedToCore || !activeProvider || wrongProvider}
            aria-label={t("agent.chat.inputLabel")}
          />
          <div className="agent-composer__footer">
            {activeProvider ? (
              <ComposerModelPicker provider={activeProvider} disabled={sending} />
            ) : (
              <span className="agent-composer__status">{t("agent.status.disconnected")}</span>
            )}
            {sending ? (
              <Button size="sm" onClick={onCancel} title={t("agent.chat.stop")}>
                <Square size={12} />
                {t("agent.chat.stop")}
              </Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                iconOnly
                onClick={onSend}
                disabled={!canSend}
                title={t("agent.chat.send")}
                aria-label={t("agent.chat.send")}
              >
                <Send size={13} />
              </Button>
            )}
          </div>
        </div>
        <p className="agent-composer__hint">
          {sending ? t("agent.chat.hintStop") : t("agent.chat.hintSend")}
        </p>
      </div>
    </>
  );
};
