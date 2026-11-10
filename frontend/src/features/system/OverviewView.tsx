import React from "react";
import { useNavigate } from "react-router-dom";
import { ArrowRight, Code2, Cpu, Radio, Settings, Sparkles, Terminal, Wrench } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { useConnectionStore } from "../connection/useConnectionStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { SystemCard } from "./SystemCard";
import { UpdateCard } from "./UpdateCard";
import { useSystemStore } from "./useSystemStore";
import { useT } from "../../lib/i18n";

export const OverviewView: React.FC = () => {
  const t = useT();
  const navigate = useNavigate();

  const info = useSystemStore((s) => s.info);
  const status = useConnectionStore((s) => s.status);
  const devMode = usePreferencesStore((s) => s.devMode);

  const formatUptime = (sec: number): string => {
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    return h > 0 ? t("system.uptime.hm", { h, m }) : t("system.uptime.ms", { m, s: sec % 60 });
  };

  const handleShortcutKeyDown = (e: React.KeyboardEvent, to: string) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      navigate(to);
    }
  };

  return (
    <div className="dashboard-layout">
      {/* Welcome & Status Hero */}
      <div className="dashboard-hero">
        <div>
          <h1 className="dashboard-hero-title">{t("overview.welcome")}</h1>
          <div className="dashboard-hero-meta">
            {info?.build && <Badge>v{info.build.version}</Badge>}
            {info?.build && (
              <span>
                {info.build.os}/{info.build.arch}
              </span>
            )}
            {info?.runtime && (
              <span className="dashboard-hero-meta-item">
                <Cpu size={13} />
                {t("system.uptime")}: {formatUptime(info.runtime.uptime_seconds)}
              </span>
            )}
          </div>
        </div>
        <div className="dashboard-hero-badges">
          <Badge>
            <Radio size={12} className="dashboard-badge-icon" />
            {t(`status.${status}`)}
          </Badge>
          {devMode && <Badge>{t("settings.devMode.badge")}</Badge>}
        </div>
      </div>

      {/* Main Balanced Bento Layout */}
      <div className="overview-bento">
        {/* Left: Quick Action Shortcuts */}
        <section className="overview-shortcuts-section" aria-label={t("overview.quickActions")}>
          <div className="dashboard-grid">
            {/* Watermark Remover Shortcut */}
            <div
              className="dashboard-action-card"
              role="button"
              tabIndex={0}
              onClick={() => navigate("/watermark")}
              onKeyDown={(e) => handleShortcutKeyDown(e, "/watermark")}
            >
              <div className="dashboard-action-header">
                <span className="dashboard-action-icon">
                  <Sparkles size={18} />
                </span>
                <ArrowRight size={16} className="dashboard-action-arrow" />
              </div>
              <div>
                <h3 className="dashboard-action-title">{t("overview.action.watermark")}</h3>
                <p className="dashboard-action-desc">{t("overview.action.watermarkDesc")}</p>
              </div>
            </div>

            {/* Tools Shortcut */}
            <div
              className="dashboard-action-card"
              role="button"
              tabIndex={0}
              onClick={() => navigate("/settings?tab=tools")}
              onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=tools")}
            >
              <div className="dashboard-action-header">
                <span className="dashboard-action-icon">
                  <Wrench size={18} />
                </span>
                <ArrowRight size={16} className="dashboard-action-arrow" />
              </div>
              <div>
                <h3 className="dashboard-action-title">{t("overview.action.tools")}</h3>
                <p className="dashboard-action-desc">{t("overview.action.toolsDesc")}</p>
              </div>
            </div>

            {/* Logs Shortcut */}
            <div
              className="dashboard-action-card"
              role="button"
              tabIndex={0}
              onClick={() => navigate("/settings?tab=logs")}
              onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=logs")}
            >
              <div className="dashboard-action-header">
                <span className="dashboard-action-icon">
                  <Terminal size={18} />
                </span>
                <ArrowRight size={16} className="dashboard-action-arrow" />
              </div>
              <div>
                <h3 className="dashboard-action-title">{t("overview.action.logs")}</h3>
                <p className="dashboard-action-desc">{t("overview.action.logsDesc")}</p>
              </div>
            </div>

            {/* Settings Shortcut */}
            <div
              className="dashboard-action-card"
              role="button"
              tabIndex={0}
              onClick={() => navigate("/settings?tab=general")}
              onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=general")}
            >
              <div className="dashboard-action-header">
                <span className="dashboard-action-icon">
                  <Settings size={18} />
                </span>
                <ArrowRight size={16} className="dashboard-action-arrow" />
              </div>
              <div>
                <h3 className="dashboard-action-title">{t("overview.action.settings")}</h3>
                <p className="dashboard-action-desc">{t("overview.action.settingsDesc")}</p>
              </div>
            </div>

            {/* DevTools Shortcut (spans full width cleanly when devMode enabled) */}
            {devMode && (
              <div
                className="dashboard-action-card dashboard-action-card--wide"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/settings?tab=devtools")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=devtools")}
              >
                <div className="dashboard-action-body">
                  <span className="dashboard-action-icon">
                    <Code2 size={18} />
                  </span>
                  <div>
                    <h3 className="dashboard-action-title">{t("settings.tabs.devtools")}</h3>
                    <p className="dashboard-action-desc">{t("settings.devMode.desc")}</p>
                  </div>
                </div>
                <ArrowRight size={16} className="dashboard-action-arrow" />
              </div>
            )}
          </div>
        </section>

        {/* Right: Runtime Telemetry & Updates */}
        <aside className="overview-side-stack">
          <SystemCard />
          <UpdateCard />
        </aside>
      </div>
    </div>
  );
};

export default OverviewView;
