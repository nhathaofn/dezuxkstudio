import React, { useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Folder,
  FolderOpen,
  Globe,
  KeyRound,
  Monitor,
  Moon,
  RotateCcw,
  Sliders,
  Sun,
  Terminal,
  Wrench,
} from "lucide-react";
import { ThemeValues, LanguageValues, type Language, type Theme } from "../../contracts/generated";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { getBridge, isDesktop } from "../../lib/bridge";
import { toActionError, type ActionError } from "../../lib/rtClient";
import { BinariesView } from "../binaries/BinariesView";
import { ProvidersView } from "../providers/ProvidersView";
import { ConnectionCard } from "../connection/ConnectionCard";
import { DiagnosticsCard } from "../diagnostics/DiagnosticsCard";
import { LiveLogView } from "../logs/LiveLogView";
import { SystemCard } from "../system/SystemCard";
import { useSystemStore } from "../system/useSystemStore";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "./usePreferencesStore";

const TABS = ["general", "tools", "providers", "logs"] as const;
type TabKey = (typeof TABS)[number];

export const SettingsView: React.FC = () => {
  const t = useT();
  const [searchParams, setSearchParams] = useSearchParams();

  const prefs = usePreferencesStore((s) => s.prefs);
  const update = usePreferencesStore((s) => s.update);
  const prefError = usePreferencesStore((s) => s.error);
  const sysInfo = useSystemStore((s) => s.info);
  const openFolder = useSystemStore((s) => s.openFolder);

  const [workspaceError, setWorkspaceError] = useState<ActionError | null>(null);
  const activeWorkspaceError = workspaceError ?? prefError;

  const isDesktopApp = isDesktop() && sysInfo?.mode === "desktop";
  const activeWorkspaceRoot = sysInfo?.paths?.workspace_root ?? "";
  const isCustomRoot = Boolean(prefs.workspace_root && prefs.workspace_root.trim() !== "");

  const handleChangeWorkspaceRoot = async () => {
    const bridge = getBridge();
    if (!bridge?.BrowseDirectory) return;
    setWorkspaceError(null);
    try {
      const chosen = await bridge.BrowseDirectory(t("settings.workspaceRoot.browseTitle"));
      if (chosen) {
        await update({ workspace_root: chosen });
      }
    } catch (err) {
      const actionErr = toActionError(err);
      setWorkspaceError(actionErr);
      console.error("Browse workspace root failed:", err);
    }
  };

  const handleResetDefaultWorkspaceRoot = async () => {
    setWorkspaceError(null);
    try {
      await update({ workspace_root: "" });
    } catch (err) {
      const actionErr = toActionError(err);
      setWorkspaceError(actionErr);
      console.error("Reset workspace root failed:", err);
    }
  };

  const handleOpenWorkspaceRoot = async () => {
    setWorkspaceError(null);
    const err = await openFolder("workspace_root");
    if (err) {
      setWorkspaceError(err);
    }
  };

  const rawTab = searchParams.get("tab");
  const effectiveTab: TabKey =
    rawTab && TABS.includes(rawTab as TabKey) ? (rawTab as TabKey) : "general";

  const setTab = (tab: TabKey) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set("tab", tab);
      return next;
    });
  };

  return (
    <div className="settings-layout">
      {/* Header & Internal Navigation Tabs */}
      <div className="settings-header-row">
        <h1 className="settings-title">{t("settings.title")}</h1>

        <nav className="settings-nav" aria-label={t("settings.nav.ariaLabel")}>
          <button
            type="button"
            className={`settings-nav-btn ${effectiveTab === "general" ? "active" : ""}`}
            onClick={() => setTab("general")}
          >
            <Sliders size={14} />
            {t("settings.tabs.general")}
          </button>

          <button
            type="button"
            className={`settings-nav-btn ${effectiveTab === "tools" ? "active" : ""}`}
            onClick={() => setTab("tools")}
          >
            <Wrench size={14} />
            {t("settings.tabs.tools")}
          </button>

          <button
            type="button"
            className={`settings-nav-btn ${effectiveTab === "logs" ? "active" : ""}`}
            onClick={() => setTab("logs")}
          >
            <Terminal size={14} />
            {t("settings.tabs.logs")}
          </button>

          <button
            type="button"
            className={`settings-nav-btn ${effectiveTab === "providers" ? "active" : ""}`}
            onClick={() => setTab("providers")}
          >
            <KeyRound size={14} />
            {t("settings.tabs.providers")}
          </button>
        </nav>
      </div>

      {/* Tab Content */}
      <div className="settings-content">
        {effectiveTab === "general" && (
          <div className="scrollable-pane">
            <div className="settings-general-grid">
              {/* Preferences configuration card */}
              <Card icon={Sliders} title={t("settings.tabs.general")} className="card--flex-fill">
                <div className="settings-rows-list">
                  {/* Theme row */}
                  <div className="settings-row">
                    <div className="settings-row-info">
                      <span className="settings-row-title">{t("settings.theme.title")}</span>
                      <span className="settings-row-desc">{t("settings.theme.desc")}</span>
                    </div>
                    <div className="settings-btn-group">
                      {ThemeValues.map((themeOption) => {
                        const isSelected = prefs.theme === themeOption;
                        const Icon =
                          themeOption === "light" ? Sun : themeOption === "dark" ? Moon : Monitor;
                        return (
                          <Button
                            key={themeOption}
                            size="sm"
                            variant={isSelected ? "primary" : "secondary"}
                            onClick={() => void update({ theme: themeOption as Theme })}
                          >
                            <Icon size={13} />
                            {t(`theme.${themeOption}`)}
                          </Button>
                        );
                      })}
                    </div>
                  </div>

                  {/* Language row */}
                  <div className="settings-row">
                    <div className="settings-row-info">
                      <span className="settings-row-title">{t("settings.language.title")}</span>
                      <span className="settings-row-desc">{t("settings.language.desc")}</span>
                    </div>
                    <div className="settings-btn-group">
                      {LanguageValues.map((langOption) => {
                        const isSelected = prefs.language === langOption;
                        return (
                          <Button
                            key={langOption}
                            size="sm"
                            variant={isSelected ? "primary" : "secondary"}
                            onClick={() => void update({ language: langOption as Language })}
                          >
                            <Globe size={13} />
                            {t(`language.${langOption}`)}
                          </Button>
                        );
                      })}
                    </div>
                  </div>

                  {/* Workspace Root row */}
                  <div className="settings-row settings-row--workspace">
                    <div className="settings-row-info settings-row-info--workspace">
                      <span className="settings-row-title">
                        {t("settings.workspaceRoot.title")}
                      </span>
                      <span className="settings-row-desc">{t("settings.workspaceRoot.desc")}</span>
                    </div>
                    <div className="settings-workspace-controls">
                      <div className="settings-workspace-path-row">
                        <span
                          className="truncate settings-workspace-path"
                          title={activeWorkspaceRoot}
                        >
                          {activeWorkspaceRoot || t("settings.workspaceRoot.notSet")}
                        </span>
                        <Badge className={isCustomRoot ? "agent-badge-online" : ""}>
                          {isCustomRoot
                            ? t("settings.workspaceRoot.custom")
                            : t("settings.workspaceRoot.default")}
                        </Badge>
                      </div>
                      <div
                        className="settings-btn-group"
                        title={!isDesktopApp ? t("system.desktopOnly") : undefined}
                      >
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => void handleOpenWorkspaceRoot()}
                          disabled={!isDesktopApp}
                        >
                          <Folder size={13} />
                          {t("settings.workspaceRoot.open")}
                        </Button>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => void handleChangeWorkspaceRoot()}
                          disabled={!isDesktopApp}
                        >
                          <FolderOpen size={13} />
                          {t("settings.workspaceRoot.change")}
                        </Button>
                        {isCustomRoot && (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() => void handleResetDefaultWorkspaceRoot()}
                            disabled={!isDesktopApp}
                          >
                            <RotateCcw size={13} />
                            {t("settings.workspaceRoot.useDefault")}
                          </Button>
                        )}
                      </div>
                      {activeWorkspaceError && (
                        <div className="settings-workspace-error">
                          <ErrorText error={activeWorkspaceError} />
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              </Card>

              {/* System Runtime Card */}
              <SystemCard />

              {/* Core Connection & Diagnostics */}
              <ConnectionCard />
              <DiagnosticsCard />
            </div>
          </div>
        )}

        {effectiveTab === "tools" && <BinariesView />}
        {effectiveTab === "providers" && <ProvidersView />}

        {effectiveTab === "logs" && <LiveLogView />}
      </div>
    </div>
  );
};

export default SettingsView;
