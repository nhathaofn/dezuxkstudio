import React from "react";
import { useSearchParams } from "react-router-dom";
import { Globe, KeyRound, Monitor, Moon, Sliders, Sun, Terminal, Wrench } from "lucide-react";
import { ThemeValues, LanguageValues, type Language, type Theme } from "../../contracts/generated";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { BinariesView } from "../binaries/BinariesView";
import { ProvidersView } from "../providers/ProvidersView";
import { ConnectionCard } from "../connection/ConnectionCard";
import { DiagnosticsCard } from "../diagnostics/DiagnosticsCard";
import { LiveLogView } from "../logs/LiveLogView";
import { SystemCard } from "../system/SystemCard";
import { useT } from "../../lib/i18n";
import { usePreferencesStore } from "./usePreferencesStore";

const TABS = ["general", "tools", "providers", "logs"] as const;
type TabKey = (typeof TABS)[number];

export const SettingsView: React.FC = () => {
  const t = useT();
  const [searchParams, setSearchParams] = useSearchParams();

  const prefs = usePreferencesStore((s) => s.prefs);
  const update = usePreferencesStore((s) => s.update);

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
