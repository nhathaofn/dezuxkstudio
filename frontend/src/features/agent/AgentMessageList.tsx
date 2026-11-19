import React, { useState } from "react";
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
  Sparkles,
  Wrench,
  X,
} from "lucide-react";
import { Button } from "../../components/ui/Button";
import { VirtualList, type VirtualListHandle } from "../../components/ui/VirtualList";
import type { AgentProviderInfo, SubagentInvocation } from "../../contracts/generated";
import { useFlash } from "../../lib/hooks/useFlash";
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";
import { Markdown } from "./markdown";
import { useScreenContextStore } from "./screenContext";
import { toolLabel } from "./toolLabels";
import { COPY_FEEDBACK_MS, useAgentStore, type AgentChatMessage } from "./useAgentStore";

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
    const [dismissed, setDismissed] = useState(false);

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
          !dismissed ? (
            <div className="agent-msg__error" role="alert">
              <span>{errorText}</span>
              <div className="agent-msg__error-actions">
                {canRetry && (
                  <Button size="sm" onClick={onRetry}>
                    <RotateCw size={12} />
                    {t("agent.chat.retry")}
                  </Button>
                )}
                <button
                  type="button"
                  className="agent-msg__error-dismiss"
                  onClick={() => setDismissed(true)}
                  title={t("dialog.close")}
                  aria-label={t("dialog.close")}
                >
                  <X size={12} />
                </button>
              </div>
            </div>
          ) : null
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

export interface AgentMessageListProps {
  listRef: React.RefObject<VirtualListHandle | null>;
  providers: AgentProviderInfo[];
  messages: AgentChatMessage[];
  activeProvider: AgentProviderInfo | undefined;
  sending: boolean;
  canRetry: boolean;
  showJump: boolean;
  onScroll: (scrollTop: number, e: React.UIEvent<HTMLDivElement>) => void;
  onPickSuggestion: (text: string) => void;
  onGoConnections: () => void;
  onRetry: () => void;
  onScrollToBottom: () => void;
}

export const AgentMessageList: React.FC<AgentMessageListProps> = ({
  listRef,
  providers,
  messages,
  activeProvider,
  sending,
  canRetry,
  showJump,
  onScroll,
  onPickSuggestion,
  onGoConnections,
  onRetry,
  onScrollToBottom,
}) => {
  const t = useT();
  const lastMsg = messages[messages.length - 1];
  return (
    <div className="agent-chat-scroll">
      {messages.length === 0 ? (
        <div
          className="agent-chat-messages scrollable-pane"
          role="log"
          aria-live="polite"
          aria-busy={sending}
        >
          <EmptyState
            provider={activeProvider}
            onPick={onPickSuggestion}
            onConnect={onGoConnections}
          />
        </div>
      ) : (
        <VirtualList<AgentChatMessage>
          ref={listRef}
          items={messages}
          estimateSize={96}
          variableHeight
          height="100%"
          getItemKey={(m) => m.id}
          onScroll={onScroll}
          className="agent-chat-messages scrollable-pane"
          innerClassName="agent-msg-list"
          role="log"
          renderItem={(m) => (
            <div style={{ paddingBottom: "var(--space-2)" }}>
              <ChatBubble
                msg={m}
                agentName={
                  providers.find((p) => p.id === m.providerId)?.name ??
                  activeProvider?.name ??
                  "Agent"
                }
                canRetry={canRetry && m === lastMsg}
                onRetry={onRetry}
              />
            </div>
          )}
        />
      )}

      {showJump && messages.length > 0 && (
        <button
          type="button"
          className="agent-jump-latest"
          onClick={onScrollToBottom}
          aria-label={t("agent.chat.jumpLatest")}
        >
          <ArrowDown size={12} />
          {t("agent.chat.jumpLatest")}
        </button>
      )}
    </div>
  );
};
