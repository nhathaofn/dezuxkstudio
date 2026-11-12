import { appConfig } from "../../config/appConfig";
import React from "react";
import { useFlash } from "../../lib/hooks/useFlash";
import { ArrowLeft, Loader2, MessageSquare, Trash2 } from "lucide-react";
import type { AgentConversation } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useAgentStore } from "./useAgentStore";

/** How long the "confirm delete" state of a history item stays armed. */
const CONFIRM_DELETE_MS = appConfig.ui.confirmDeleteMs;

function formatWhen(ms: number, lang: string): string {
  const d = new Date(ms);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay
    ? d.toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString(lang, { day: "2-digit", month: "2-digit", year: "numeric" });
}

const HistoryItem: React.FC<{ conv: AgentConversation; current: boolean; disabled: boolean }> = ({
  conv,
  current,
  disabled,
}) => {
  const t = useT();
  const lang = usePreferencesStore((s) => s.prefs.language);
  const providers = useAgentStore((s) => s.providers);
  const openConversation = useAgentStore((s) => s.openConversation);
  const deleteConversation = useAgentStore((s) => s.deleteConversation);
  const [confirm, armConfirm, disarmConfirm] = useFlash(CONFIRM_DELETE_MS);

  const provider = providers.find((p) => p.id === conv.provider_id)?.name ?? conv.provider_id;

  return (
    <li className={`agent-history-item${current ? " is-current" : ""}`}>
      <button
        type="button"
        className="agent-history-item__open"
        onClick={() => void openConversation(conv.id)}
        disabled={disabled}
        title={conv.title}
      >
        <span className="agent-history-item__title">
          {conv.title || t("agent.history.untitled")}
        </span>
        <span className="agent-history-item__meta">
          {formatWhen(conv.updated_at, lang)}
          {provider ? ` · ${provider}` : ""}
          {` · ${t("agent.history.messages", { count: conv.message_count })}`}
          {current ? ` · ${t("agent.history.current")}` : ""}
        </span>
      </button>
      {confirm ? (
        <Button
          size="sm"
          className="agent-clear-confirm"
          onClick={() => {
            disarmConfirm();
            void deleteConversation(conv.id);
          }}
          disabled={disabled}
        >
          {t("agent.history.confirmDelete")}
        </Button>
      ) : (
        <Button
          size="sm"
          iconOnly
          onClick={() => armConfirm(true)}
          disabled={disabled}
          title={t("agent.history.delete")}
          aria-label={t("agent.history.delete")}
        >
          <Trash2 size={12} />
        </Button>
      )}
    </li>
  );
};

/** Earlier chats; opening one makes it the current chat so it can be read and continued. */
export const AgentHistoryList: React.FC = () => {
  const t = useT();
  const conversations = useAgentStore((s) => s.conversations);
  const loading = useAgentStore((s) => s.loadingConversations);
  const conversationId = useAgentStore((s) => s.conversationId);
  const sending = useAgentStore((s) => s.sending);
  const setChatView = useAgentStore((s) => s.setChatView);

  return (
    <div className="agent-history scrollable-pane">
      <div className="agent-history__bar">
        <Button size="sm" onClick={() => setChatView("chat")}>
          <ArrowLeft size={12} />
          {t("agent.history.back")}
        </Button>
        <span className="agent-history__title">{t("agent.history.title")}</span>
        {loading && <Loader2 size={13} className="animate-spin" />}
      </div>
      {sending && <p className="agent-history__note">{t("agent.history.busy")}</p>}
      {conversations.length === 0 && !loading ? (
        <div className="agent-empty-state">
          <MessageSquare size={24} className="agent-empty-state__icon" />
          <p className="agent-empty-state__desc">{t("agent.history.empty")}</p>
        </div>
      ) : (
        <ul className="agent-history__list">
          {conversations.map((c) => (
            <HistoryItem key={c.id} conv={c} current={c.id === conversationId} disabled={sending} />
          ))}
        </ul>
      )}
    </div>
  );
};
