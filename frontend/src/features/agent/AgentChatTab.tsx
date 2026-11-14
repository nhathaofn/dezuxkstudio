import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  ArrowDown,
  Bot,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  Loader2,
  PlugZap,
  RotateCw,
  Send,
  ShieldAlert,
  Sparkles,
  Square,
  Wrench,
  X,
} from "lucide-react";
import type { AgentProviderInfo, SubagentInvocation } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";
import { AgentHistoryList } from "./AgentHistoryList";
import { Markdown } from "./markdown";
import { useScreenContextStore } from "./screenContext";
import { toolLabel } from "./toolLabels";
import { useFlash } from "../../lib/hooks/useFlash";
import { useAgentCustomizationsStore } from "./useAgentCustomizationsStore";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import { COPY_FEEDBACK_MS, useAgentStore, type AgentChatMessage } from "./useAgentStore";
import { appConfig } from "../../config/appConfig";

export interface AgentChatTabProps {
  connectedToCore: boolean;
}

/** Distance from the bottom (px) within which the list keeps following new content. */
const STICK_TO_BOTTOM_PX = 80;
/** The composer grows with its content up to this height (px), then scrolls. */
const COMPOSER_MAX_HEIGHT = appConfig.ui.composerMaxHeight;

/** Summary chip for a reply's tool calls; the list opens in the detail panel below the chips. */
const ToolsChip: React.FC<{
  tools: NonNullable<AgentChatMessage["tools"]>;
  open: boolean;
  onToggle: () => void;
}> = ({ tools, open, onToggle }) => {
  const t = useT();
  const running = tools.some((tool) => tool.status === "running");
  const failed = tools.filter((tool) => tool.status === "error").length;
  return (
    <button
      type="button"
      className={`agent-chip${open ? " is-open" : ""}${failed > 0 ? " agent-chip--error" : ""}`}
      onClick={onToggle}
      aria-expanded={open}
    >
      {running ? <Loader2 size={11} className="animate-spin" /> : <Wrench size={11} />}
      <span>
        {running
          ? t("agent.chat.toolsRunning")
          : t("agent.chat.toolsDone", { count: tools.length })}
        {failed > 0 && ` · ${t("agent.chat.toolsFailed", { count: failed })}`}
      </span>
      {open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
    </button>
  );
};

const ToolList: React.FC<{ tools: NonNullable<AgentChatMessage["tools"]> }> = ({ tools }) => {
  const t = useT();
  return (
    <div className="agent-tools-row" aria-label={t("agent.chat.toolsUsed")}>
      {tools.map((tool, idx) => (
        <span
          key={`${tool.name}-${idx}`}
          className={`agent-tool-pill agent-tool-pill--${tool.status}`}
          title={tool.name}
        >
          {tool.status === "running" ? (
            <Loader2 size={10} className="animate-spin" />
          ) : tool.status === "error" ? (
            <X size={10} />
          ) : (
            <Wrench size={10} />
          )}
          <span>{toolLabel(t, tool.name)}</span>
        </span>
      ))}
    </div>
  );
};

const SubagentCard: React.FC<{ item: SubagentInvocation }> = ({ item }) => {
  const t = useT();
  const [open, setOpen] = useState(item.status !== "done");
  const isRunning = item.status === "running";
  const isError = item.status === "error";
  const statusText = isRunning
    ? t("agent.subagent.running", { name: item.name })
    : isError
      ? t("agent.subagent.error", { name: item.name })
      : t("agent.subagent.done", { name: item.name });
  const toolCount = item.tools?.length ?? 0;

  return (
    <div className={`agent-subagent-card agent-subagent-card--${item.status}`}>
      <button
        type="button"
        className="agent-subagent-card__header"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="agent-subagent-card__chev">
          {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
        </span>
        <span className="agent-subagent-card__icon">
          {isRunning ? (
            <Loader2 size={13} className="animate-spin" />
          ) : isError ? (
            <AlertCircle size={13} />
          ) : (
            <Check size={13} />
          )}
        </span>
        <span className="agent-subagent-card__title">{statusText}</span>
        {item.provider && <span className="agent-subagent-card__badge">{item.provider}</span>}
        {toolCount > 0 && (
          <span className="agent-subagent-card__count">
            {t("agent.subagent.tools", { count: toolCount })}
          </span>
        )}
      </button>
      {open && (
        <div className="agent-subagent-card__body">
          {item.tools && item.tools.length > 0 && (
            <div className="agent-tools-list">
              {item.tools.map((toolName, i) => (
                <span
                  key={`${toolName}-${i}`}
                  className="agent-tool-pill agent-tool-pill--done"
                  title={toolName}
                >
                  <Wrench size={10} />
                  <span>{toolLabel(t, toolName)}</span>
                </span>
              ))}
            </div>
          )}
          {item.error && <div className="agent-subagent-card__error">{item.error}</div>}
          {item.text && (
            <div className={`agent-subagent-card__text${isRunning ? " is-streaming" : ""}`}>
              <Markdown text={item.text} />
            </div>
          )}
        </div>
      )}
    </div>
  );
};

interface ChatBubbleProps {
  msg: AgentChatMessage;
  agentName: string;
  canRetry: boolean;
  onRetry: () => void;
}

const ChatBubble: React.FC<ChatBubbleProps> = React.memo(
  ({ msg, agentName, canRetry, onRetry }) => {
    const t = useT();
    // Tools and reasoning share one compact row of chips; at most one detail panel is open.
    const [detail, setDetail] = useState<"tools" | "thought" | null>(null);
    const toggle = (d: "tools" | "thought") => setDetail((cur) => (cur === d ? null : d));
    const [copied, flashCopied] = useFlash(COPY_FEEDBACK_MS);

    const isUser = msg.role === "user";
    const isError = msg.role === "error";
    const thinkingOnly = msg.streaming && !msg.text;
    const hasTools = !!msg.tools && msg.tools.length > 0;

    const handleCopy = async () => {
      try {
        await navigator.clipboard.writeText(msg.text);
        flashCopied(true);
      } catch {
        // Clipboard is unavailable outside a secure context; nothing to do.
      }
    };

    if (isUser) {
      return (
        <div className="agent-msg agent-msg--user">
          <div className="agent-msg__bubble">{msg.text}</div>
        </div>
      );
    }

    const errorText = isError
      ? (translateServerMessage(t, msg.errorKey, msg.errorParams) ?? msg.text)
      : "";

    return (
      <div className={`agent-msg agent-msg--${msg.role}`}>
        <div className="agent-msg__header">
          <span className="agent-msg__role">
            <Bot size={12} />
            <span>{agentName}</span>
          </span>
          {msg.text && !msg.streaming && !isError && (
            <button
              type="button"
              className="agent-msg__action"
              onClick={() => void handleCopy()}
              title={copied ? t("agent.chat.copied") : t("agent.chat.copy")}
              aria-label={t("agent.chat.copy")}
            >
              {copied ? <Check size={12} /> : <Copy size={12} />}
            </button>
          )}
        </div>

        {(hasTools || msg.thought) && (
          <div className="agent-msg__meta">
            {hasTools && (
              <ToolsChip
                tools={msg.tools!}
                open={detail === "tools"}
                onToggle={() => toggle("tools")}
              />
            )}
            {msg.thought && (
              <button
                type="button"
                className={`agent-chip${detail === "thought" ? " is-open" : ""}`}
                onClick={() => toggle("thought")}
                aria-expanded={detail === "thought"}
              >
                <Sparkles size={11} />
                <span>
                  {thinkingOnly ? t("agent.chat.thinking") : t("agent.chat.thinkingProcess")}
                </span>
                {thinkingOnly ? (
                  <span className="agent-dots" aria-hidden="true" />
                ) : detail === "thought" ? (
                  <ChevronDown size={11} />
                ) : (
                  <ChevronRight size={11} />
                )}
              </button>
            )}
          </div>
        )}
        {detail === "tools" && hasTools && (
          <div className="agent-msg__detail">
            <ToolList tools={msg.tools!} />
          </div>
        )}
        {detail === "thought" && msg.thought && (
          <pre className="agent-msg__detail agent-thought__body">{msg.thought}</pre>
        )}

        {msg.subagents && msg.subagents.length > 0 && (
          <div className="agent-subagents-list">
            {msg.subagents.map((sa) => (
              <SubagentCard key={sa.id} item={sa} />
            ))}
          </div>
        )}

        {isError ? (
          <div className="agent-msg__error" role="alert">
            <span>{errorText}</span>
            {canRetry && (
              <Button size="sm" onClick={onRetry}>
                <RotateCw size={12} />
                {t("agent.chat.retry")}
              </Button>
            )}
          </div>
        ) : msg.text ? (
          <div className={`agent-msg__body${msg.streaming ? " is-streaming" : ""}`}>
            <Markdown text={msg.text} />
          </div>
        ) : msg.streaming && !msg.thought && !(msg.subagents && msg.subagents.length > 0) ? (
          <div className="agent-msg__body agent-typing">
            {t("agent.chat.thinking")}
            <span className="agent-dots" aria-hidden="true" />
          </div>
        ) : null}
      </div>
    );
  }
);
ChatBubble.displayName = "ChatBubble";

/** Example requests shown on an empty chat; the first one adapts to the current screen. */
const SUGGESTIONS: MessageKey[] = [
  "agent.chat.suggest.capabilities",
  "agent.chat.suggest.subtitles",
  "agent.chat.suggest.organize",
];

const EmptyState: React.FC<{
  provider: AgentProviderInfo | undefined;
  onPick: (text: string) => void;
  onConnect: () => void;
}> = ({ provider, onPick, onConnect }) => {
  const t = useT();
  const screen = useScreenContextStore((s) => s.context);
  const developerMode = useAgentStore((s) => s.developerMode);

  if (!provider) {
    return (
      <div className="agent-empty-state">
        <Bot size={28} className="agent-empty-state__icon" />
        <div className="agent-empty-state__title">{t("agent.chat.emptyTitle")}</div>
        <p className="agent-empty-state__desc">{t("agent.chat.emptyDesc")}</p>
        <Button variant="primary" size="sm" onClick={onConnect}>
          <PlugZap size={13} />
          {t("agent.chat.goConnections")}
        </Button>
      </div>
    );
  }

  const suggestions = SUGGESTIONS.map((key) => t(key));
  if (screen?.screen && (screen.selected_files?.length ?? 0) > 0) {
    suggestions.unshift(
      t("agent.chat.suggest.screen", {
        screen: screen.screen,
        count: screen.selected_files?.length ?? 0,
      })
    );
  }

  return (
    <div className="agent-empty-state">
      <Sparkles size={26} className="agent-empty-state__icon agent-empty-state__icon--accent" />
      <div className="agent-empty-state__title">
        {t("agent.chat.readyTitle", { name: provider.name })}
      </div>
      <p className="agent-empty-state__desc">
        {t(developerMode ? "agent.chat.readyDescDev" : "agent.chat.readyDesc")}
      </p>
      <div className="agent-suggestions" role="list">
        {suggestions.slice(0, 4).map((s) => (
          <button
            key={s}
            type="button"
            role="listitem"
            className="agent-suggestion"
            onClick={() => onPick(s)}
          >
            {s}
          </button>
        ))}
      </div>
    </div>
  );
};

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

export const AgentChatTab: React.FC<AgentChatTabProps> = ({ connectedToCore }) => {
  const chatView = useAgentDrawerStore((s) => s.chatView);
  return chatView === "history" ? (
    <AgentHistoryList />
  ) : (
    <AgentChatPane connectedToCore={connectedToCore} />
  );
};

const AgentChatPane: React.FC<AgentChatTabProps> = ({ connectedToCore }) => {
  const t = useT();
  const providers = useAgentStore((s) => s.providers);
  const conversationId = useAgentStore((s) => s.conversationId);
  const conversations = useAgentStore((s) => s.conversations);
  const messages = useAgentStore((s) => s.messages);
  const approvals = useAgentStore((s) => s.approvals);
  const prompt = useAgentStore((s) => s.prompt);
  const skills = useAgentCustomizationsStore((s) => s.skills);
  const sending = useAgentStore((s) => s.sending);
  const busyAction = useAgentStore((s) => s.busyAction);
  const setPrompt = useAgentStore((s) => s.setPrompt);
  const setActiveTab = useAgentDrawerStore((s) => s.setActiveTab);
  const connectProvider = useAgentStore((s) => s.connectProvider);
  const newChat = useAgentStore((s) => s.newChat);
  const sendPrompt = useAgentStore((s) => s.sendPrompt);
  const cancelPrompt = useAgentStore((s) => s.cancelPrompt);
  const retryLast = useAgentStore((s) => s.retryLast);
  const resolveApproval = useAgentStore((s) => s.resolveApproval);

  const listRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const stickRef = useRef(true);
  const [showJump, setShowJump] = useState(false);
  const [slashIdx, setSlashIdx] = useState(0);
  const [slashDismissedFor, setSlashDismissedFor] = useState<string | null>(null);
  const activeProvider = providers.find((p) => p.active);
  const convOwnerId = conversationId
    ? (messages.find((m) => m.providerId)?.providerId ??
      conversations.find((c) => c.id === conversationId)?.provider_id)
    : undefined;
  const convOwner = convOwnerId ? providers.find((p) => p.id === convOwnerId) : undefined;
  const wrongProvider = Boolean(activeProvider && convOwnerId && convOwnerId !== activeProvider.id);
  const convOwnerName = convOwner?.name ?? convOwnerId ?? "";
  const canSend =
    connectedToCore && !!activeProvider && !wrongProvider && !!prompt.trim() && !sending;

  const slashQuery = useMemo(() => {
    if (slashDismissedFor !== null && slashDismissedFor === prompt) return null;
    const m = /^\/([a-z0-9_-]*)$/i.exec(prompt);
    return m ? m[1].toLowerCase() : null;
  }, [prompt, slashDismissedFor]);

  const slashSkills = useMemo(() => {
    if (slashQuery === null) return [];
    return skills
      .filter((sk) => !sk.tombstoned && sk.enabled && sk.user_invocable)
      .filter(
        (sk) =>
          !slashQuery ||
          sk.name.toLowerCase().includes(slashQuery) ||
          sk.description.toLowerCase().includes(slashQuery)
      )
      .slice(0, 8);
  }, [skills, slashQuery]);

  const activeSlashIdx = Math.min(slashIdx, Math.max(0, slashSkills.length - 1));

  // Follow new content only while the user is at the bottom; reading older messages is not interrupted.
  const onScroll = useCallback(() => {
    const el = listRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_TO_BOTTOM_PX;
    stickRef.current = atBottom;
    setShowJump(!atBottom);
  }, []);

  const scrollToBottom = useCallback((smooth = false) => {
    const el = listRef.current;
    if (!el) return;
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? "smooth" : "auto" });
    stickRef.current = true;
    setShowJump(false);
  }, []);

  useLayoutEffect(() => {
    if (stickRef.current) scrollToBottom();
  }, [messages, approvals, scrollToBottom]);

  // Grow the composer with its content.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, COMPOSER_MAX_HEIGHT)}px`;
  }, [prompt]);

  // Focus the composer when an agent becomes available.
  useEffect(() => {
    if (activeProvider && connectedToCore) inputRef.current?.focus();
  }, [activeProvider?.id, connectedToCore]); // eslint-disable-line react-hooks/exhaustive-deps

  const insertSlashSkill = useCallback(
    (name: string) => {
      setPrompt(`/${name} `);
      setSlashDismissedFor(null);
      setSlashIdx(0);
      inputRef.current?.focus();
    },
    [setPrompt]
  );

  const handlePromptChange = (nextVal: string) => {
    if (slashDismissedFor !== null && nextVal !== slashDismissedFor) {
      setSlashDismissedFor(null);
    }
    setSlashIdx(0);
    setPrompt(nextVal);
  };

  const send = () => {
    if (!canSend) return;
    stickRef.current = true; // a new question always brings the reply into view
    void sendPrompt();
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (slashSkills.length > 0 && !e.nativeEvent.isComposing) {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSlashIdx((i) => (i + 1) % slashSkills.length);
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setSlashIdx((i) => (i - 1 + slashSkills.length) % slashSkills.length);
        return;
      }
      if ((e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) && slashSkills[activeSlashIdx]) {
        e.preventDefault();
        insertSlashSkill(slashSkills[activeSlashIdx].name);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setSlashDismissedFor(prompt);
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    } else if (e.key === "Escape" && sending) {
      e.preventDefault();
      void cancelPrompt();
    }
  };

  const pickSuggestion = (text: string) => {
    setPrompt(text);
    inputRef.current?.focus();
  };

  const lastMsg = messages[messages.length - 1];
  const canRetry = !sending && !!activeProvider && !wrongProvider && lastMsg?.role === "error";

  return (
    <div className="agent-chat-tab">
      <div className="agent-chat-scroll">
        <div
          className="agent-chat-messages scrollable-pane"
          ref={listRef}
          onScroll={onScroll}
          role="log"
          aria-live="polite"
          aria-busy={sending}
        >
          {messages.length === 0 ? (
            <EmptyState
              provider={activeProvider}
              onPick={pickSuggestion}
              onConnect={() => setActiveTab("connections")}
            />
          ) : (
            <div className="agent-msg-list">
              {messages.map((m) => (
                <ChatBubble
                  key={m.id}
                  msg={m}
                  agentName={
                    providers.find((p) => p.id === m.providerId)?.name ??
                    activeProvider?.name ??
                    "Agent"
                  }
                  canRetry={canRetry && m === lastMsg}
                  onRetry={() => void retryLast()}
                />
              ))}
            </div>
          )}
        </div>

        {showJump && messages.length > 0 && (
          <button
            type="button"
            className="agent-jump-latest"
            onClick={() => scrollToBottom(true)}
            aria-label={t("agent.chat.jumpLatest")}
          >
            <ArrowDown size={12} />
            {t("agent.chat.jumpLatest")}
          </button>
        )}
      </div>

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
                <Button size="sm" onClick={() => void resolveApproval(ap.id, false)}>
                  <X size={12} />
                  {t("agent.chat.reject")}
                </Button>
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => void resolveApproval(ap.id, true)}
                >
                  <Check size={12} />
                  {t("agent.chat.approve")}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {wrongProvider && activeProvider && (
        <div className="agent-chat-banner" role="status">
          <p className="agent-chat-banner__text">
            {t("agent.chat.wrongProviderBanner", {
              provider: convOwnerName,
              active: activeProvider.name,
            })}
          </p>
          <div className="agent-chat-banner__actions">
            {convOwner && (
              <Button
                size="sm"
                disabled={busyAction}
                onClick={() => void connectProvider(convOwner.id)}
              >
                {t("agent.chat.switchProvider", { provider: convOwner.name })}
              </Button>
            )}
            <Button variant="primary" size="sm" onClick={() => void newChat()}>
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
                    insertSlashSkill(sk.name);
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
            onChange={(e) => handlePromptChange(e.target.value)}
            onKeyDown={handleKeyDown}
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
              <Button size="sm" onClick={() => void cancelPrompt()} title={t("agent.chat.stop")}>
                <Square size={12} />
                {t("agent.chat.stop")}
              </Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                iconOnly
                onClick={send}
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
    </div>
  );
};
