import React, { useState } from "react";
import {
  Bot,
  FolderPlus,
  MessageSquare,
  PlugZap,
  RefreshCw,
  History,
  Plus,
  Sparkles,
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
import { AgentCustomizationsTab } from "./AgentCustomizationsTab";
import { AgentProviderCard } from "./AgentProviderCard";
import { useSystemStore } from "../system/useSystemStore";
import { useAgentCustomizationsStore } from "./useAgentCustomizationsStore";
import { useAgentDrawerStore } from "./useAgentDrawerStore";
import { useAgentStore } from "./useAgentStore";

export const AgentDrawer: React.FC = () => {
  const t = useT();
  const coreConnected = useConnectionStore((s) => s.status === "connected");

  const panelOpen = useAgentDrawerStore((s) => s.panelOpen);
  const panelWidth = useAgentDrawerStore((s) => s.panelWidth);
  const activeTab = useAgentDrawerStore((s) => s.activeTab);
  const chatView = useAgentDrawerStore((s) => s.chatView);
  const closePanel = useAgentDrawerStore((s) => s.closePanel);
  const setPanelWidth = useAgentDrawerStore((s) => s.setPanelWidth);
  const setActiveTab = useAgentDrawerStore((s) => s.setActiveTab);
  const setChatView = useAgentDrawerStore((s) => s.setChatView);

  const providers = useAgentStore((s) => s.providers);
  const developerMode = useAgentStore((s) => s.developerMode);
  const allowedDirs = useAgentStore((s) => s.allowedDirs);
  const loadingProviders = useAgentStore((s) => s.loadingProviders);
  const coreError = useAgentStore((s) => s.error);
  const clearCoreError = useAgentStore((s) => s.clearError);
  const customizationsError = useAgentCustomizationsStore((s) => s.error);
  const clearCustomizationsError = useAgentCustomizationsStore((s) => s.clearError);
  // Trusted folders bound what remote clients may touch, so a headless server refuses changes to them
  // (agent:add/remove_allowed_dir). Only the desktop app on a non-server core can edit the list.
  const coreMode = useSystemStore((s) => s.info?.mode);
  const canEditFolders = isDesktop() && coreMode !== "server";
  const fetchProviders = useAgentStore((s) => s.fetchProviders);
  const fetchConversations = useAgentStore((s) => s.fetchConversations);
  const newChat = useAgentStore((s) => s.newChat);
  const hasMessages = useAgentStore((s) => s.messages.length > 0);
  const sending = useAgentStore((s) => s.sending);
  const addAllowedDir = useAgentStore((s) => s.addAllowedDir);
  const removeAllowedDir = useAgentStore((s) => s.removeAllowedDir);

  const error = coreError ?? customizationsError;
  const clearError = () => {
    clearCoreError();
    clearCustomizationsError();
  };

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
          {developerMode && <Badge className="agent-badge-dev">{t("agent.mode.developer")}</Badge>}
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
              const nextView = historyOpen ? "chat" : "history";
              setActiveTab("chat");
              setChatView(nextView);
              if (nextView === "history") void fetchConversations();
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
          aria-selected={activeTab === "customizations"}
          className={`agent-drawer__tab${activeTab === "customizations" ? " is-active" : ""}`}
          onClick={() => setActiveTab("customizations")}
        >
          <Sparkles size={13} />
          <span>{t("agent.tabs.customizations")}</span>
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
          <button
            type="button"
            className="agent-drawer__error-dismiss"
            onClick={clearError}
            title={t("dialog.close")}
            aria-label={t("dialog.close")}
          >
            <X size={12} />
          </button>
        </div>
      )}

      {activeTab === "chat" ? (
        <AgentChatTab connectedToCore={coreConnected} />
      ) : activeTab === "customizations" ? (
        <AgentCustomizationsTab coreConnected={coreConnected} canEdit={canEditFolders} />
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
              {canEditFolders && (
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
                    {canEditFolders && (
                      <Button
                        size="sm"
                        iconOnly
                        onClick={() => void removeAllowedDir(dir)}
                        title={t("agent.folders.remove")}
                        aria-label={t("agent.folders.remove")}
                      >
                        <Trash2 size={12} />
                      </Button>
                    )}
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
