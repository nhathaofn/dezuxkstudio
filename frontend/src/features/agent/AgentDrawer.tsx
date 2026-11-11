import React, { useState } from "react";
import {
  Bot,
  FolderPlus,
  MessageSquare,
  PlugZap,
  RefreshCw,
  History,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ErrorText } from "../../components/ui/ErrorText";
import { getBridge, isDesktop } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { useConnectionStore } from "../connection/useConnectionStore";
import { AgentChatTab } from "./AgentChatTab";
import { AgentProviderCard } from "./AgentProviderCard";
import { useAgentStore } from "./useAgentStore";

export const AgentDrawer: React.FC = () => {
  const t = useT();
  const coreConnected = useConnectionStore((s) => s.status === "connected");

  const panelOpen = useAgentStore((s) => s.panelOpen);
  const panelWidth = useAgentStore((s) => s.panelWidth);
  const activeTab = useAgentStore((s) => s.activeTab);
  const providers = useAgentStore((s) => s.providers);
  const allowedDirs = useAgentStore((s) => s.allowedDirs);
  const loadingProviders = useAgentStore((s) => s.loadingProviders);
  const error = useAgentStore((s) => s.error);
  const closePanel = useAgentStore((s) => s.closePanel);
  const setPanelWidth = useAgentStore((s) => s.setPanelWidth);
  const setActiveTab = useAgentStore((s) => s.setActiveTab);
  const fetchProviders = useAgentStore((s) => s.fetchProviders);
  const newChat = useAgentStore((s) => s.newChat);
  const hasMessages = useAgentStore((s) => s.messages.length > 0);
  const sending = useAgentStore((s) => s.sending);
  const chatView = useAgentStore((s) => s.chatView);
  const setChatView = useAgentStore((s) => s.setChatView);
  const addAllowedDir = useAgentStore((s) => s.addAllowedDir);
  const removeAllowedDir = useAgentStore((s) => s.removeAllowedDir);

  const [resizing, setResizing] = useState(false);
  const historyOpen = activeTab === "chat" && chatView === "history";

  if (!panelOpen) return null;

  const activeProvider = providers.find((p) => p.active);

  const startResize = (e: React.PointerEvent<HTMLButtonElement>) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = panelWidth;
    let latestWidth = startWidth;

    const onMove = (moveEvt: PointerEvent) => {
      latestWidth = startWidth + (startX - moveEvt.clientX);
      setPanelWidth(latestWidth, false);
    };
    const onUp = () => {
      setPanelWidth(latestWidth, true);
      setResizing(false);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    };

    setResizing(true);
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  };

  const handleAddFolder = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseDirectory) return;
    try {
      const selected = await bridge.BrowseDirectory(t("agent.folders.browseTitle"));
      if (selected) {
        await addAllowedDir(selected);
      }
    } catch (err) {
      console.error("Browse allowed folder failed:", err);
    }
  };

  return (
    <aside
      className={`agent-drawer${resizing ? " agent-drawer--resizing" : ""}`}
      style={{ width: panelWidth }}
      aria-label={t("agent.title")}
    >
      <button
        type="button"
        className="agent-drawer__resizer"
        onPointerDown={startResize}
        aria-label={t("agent.resize")}
      />

      <div className="agent-drawer__header">
        <div className="agent-drawer__title-group">
          <Bot size={16} className="agent-drawer__icon" />
          <h2 className="agent-drawer__title">{t("agent.title")}</h2>
          <Badge className={activeProvider ? "agent-badge-online" : ""}>
            {activeProvider ? activeProvider.name : t("agent.status.disconnected")}
          </Badge>
        </div>

        <div className="agent-drawer__actions">
          {/* A new chat never deletes anything: earlier chats stay in History. */}
          <Button
            size="sm"
            iconOnly
            onClick={() => {
              setActiveTab("chat");
              void newChat();
            }}
            disabled={sending || (!hasMessages && activeTab === "chat" && chatView === "chat")}
            title={t("agent.newChat")}
            aria-label={t("agent.newChat")}
          >
            <Plus size={14} />
          </Button>
          <Button
            size="sm"
            iconOnly
            className={historyOpen ? "is-active" : ""}
            onClick={() => {
              setActiveTab("chat");
              setChatView(historyOpen ? "chat" : "history");
            }}
            aria-pressed={historyOpen}
            title={t("agent.history.title")}
            aria-label={t("agent.history.title")}
          >
            <History size={14} />
          </Button>
          <Button
            size="sm"
            iconOnly
            onClick={closePanel}
            title={t("agent.close")}
            aria-label={t("agent.close")}
          >
            <X size={14} />
          </Button>
        </div>
      </div>

      <div className="agent-drawer__tabs" role="tablist" aria-label={t("agent.title")}>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "chat"}
          className={`agent-drawer__tab${activeTab === "chat" ? " is-active" : ""}`}
          onClick={() => setActiveTab("chat")}
        >
          <MessageSquare size={13} />
          <span>{t("agent.tabs.chat")}</span>
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === "connections"}
          className={`agent-drawer__tab${activeTab === "connections" ? " is-active" : ""}`}
          onClick={() => setActiveTab("connections")}
        >
          <PlugZap size={13} />
          <span>{t("agent.tabs.connections")}</span>
        </button>
      </div>

      {error && (
        <div className="agent-drawer__error">
          <ErrorText error={error} />
        </div>
      )}

      {activeTab === "chat" ? (
        <AgentChatTab connectedToCore={coreConnected} />
      ) : (
        <div className="agent-connections-tab scrollable-pane">
          <div className="agent-connections-tab__bar">
            <p className="agent-connections-tab__subtitle">{t("agent.connections.subtitle")}</p>
            <Button
              size="sm"
              onClick={() => void fetchProviders()}
              disabled={!coreConnected || loadingProviders}
              title={t("agent.connections.refresh")}
            >
              <RefreshCw size={12} className={loadingProviders ? "animate-spin" : ""} />
              {t("agent.connections.refresh")}
            </Button>
          </div>

          <div className="agent-provider-list">
            {providers.map((provider) => (
              <AgentProviderCard key={provider.id} provider={provider} disabled={!coreConnected} />
            ))}
          </div>

          <div className="agent-folders-section">
            <div className="agent-folders-section__header">
              <div>
                <h3 className="agent-folders-section__title">{t("agent.folders.title")}</h3>
                <p className="agent-folders-section__desc">{t("agent.folders.desc")}</p>
              </div>
              {isDesktop() && (
                <Button size="sm" onClick={() => void handleAddFolder()} disabled={!coreConnected}>
                  <FolderPlus size={12} />
                  {t("agent.folders.add")}
                </Button>
              )}
            </div>

            {allowedDirs.length === 0 ? (
              <p className="agent-folders-empty">{t("agent.folders.empty")}</p>
            ) : (
              <ul className="agent-folders-list">
                {allowedDirs.map((dir) => (
                  <li key={dir} className="agent-folder-item">
                    <span className="agent-folder-item__path" title={dir}>
                      {dir}
                    </span>
                    <Button
                      size="sm"
                      iconOnly
                      onClick={() => void removeAllowedDir(dir)}
                      title={t("agent.folders.remove")}
                      aria-label={t("agent.folders.remove")}
                    >
                      <Trash2 size={12} />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </aside>
  );
};
