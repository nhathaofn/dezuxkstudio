import React, { useEffect, useState } from "react";
import {
  Bot,
  FolderPlus,
  MessageSquare,
  PlugZap,
  RefreshCw,
  RotateCcw,
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

/** How long the "confirm new chat" state stays armed. */
const CONFIRM_CLEAR_MS = 3000;

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
  const clearChat = useAgentStore((s) => s.clearChat);
  const hasMessages = useAgentStore((s) => s.messages.length > 0);
  const sending = useAgentStore((s) => s.sending);
  const addAllowedDir = useAgentStore((s) => s.addAllowedDir);
  const removeAllowedDir = useAgentStore((s) => s.removeAllowedDir);

  const [resizing, setResizing] = useState(false);
  // Clearing deletes the persisted conversation, so it takes a second click to confirm.
  const [confirmClear, setConfirmClear] = useState(false);
  useEffect(() => {
    if (!confirmClear) return;
    const timer = setTimeout(() => setConfirmClear(false), CONFIRM_CLEAR_MS);
    return () => clearTimeout(timer);
  }, [confirmClear]);

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
          {activeTab === "chat" &&
            (confirmClear ? (
              <Button
                size="sm"
                className="agent-clear-confirm"
                onClick={() => {
                  setConfirmClear(false);
                  void clearChat();
                }}
                title={t("agent.clearConfirm")}
              >
                <RotateCcw size={13} />
                {t("agent.clearConfirm")}
              </Button>
            ) : (
              <Button
                size="sm"
                iconOnly
                onClick={() => setConfirmClear(true)}
                disabled={!hasMessages || sending}
                title={t("agent.clear")}
                aria-label={t("agent.clear")}
              >
                <RotateCcw size={13} />
              </Button>
            ))}
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
