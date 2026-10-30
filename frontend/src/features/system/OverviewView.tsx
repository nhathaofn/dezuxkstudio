import React from "react";
import { useNavigate } from "react-router-dom";
import { ArrowRight, Code2, Cpu, Radio, Settings, Terminal, Wrench } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { useConnectionStore } from "../connection/useConnectionStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useSystemStore } from "./useSystemStore";
import { UpdateCard } from "./UpdateCard";
import { useT } from "../../lib/i18n";

const formatUptime = (sec: number): string => {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return h > 0 ? `${h}h ${m}m` : `${m}m ${sec % 60}s`;
};

export const OverviewView: React.FC = () => {
  const t = useT();
  const navigate = useNavigate();

  const info = useSystemStore((s) => s.info);
  const status = useConnectionStore((s) => s.status);
  const devMode = usePreferencesStore((s) => s.devMode);

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
              <span style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-1)" }}>
                <Cpu size={13} />
                {t("system.uptime")}: {formatUptime(info.runtime.uptime_seconds)}
              </span>
            )}
          </div>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
          <Badge>
            <Radio size={12} style={{ marginRight: "var(--space-1)" }} />
            {t(`status.${status}`)}
          </Badge>
          {devMode && <Badge>{t("settings.devMode.badge")}</Badge>}
        </div>
      </div>

      {/* Software Updates */}
      <UpdateCard />

      {/* Quick Action Shortcuts */}
      <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
        <h2
          style={{
            fontSize: "var(--text-lg)",
            fontWeight: "var(--weight-semibold)",
            color: "var(--text-primary)",
            margin: 0,
          }}
        >
          {t("overview.quickActions")}
        </h2>

        <div className="dashboard-grid">
          {/* Tools Shortcut */}
          <div
            className="dashboard-action-card"
            role="button"
            tabIndex={0}
            onClick={() => navigate("/settings?tab=tools")}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") navigate("/settings?tab=tools");
            }}
          >
            <div className="dashboard-action-header">
              <span className="dashboard-action-icon">
                <Wrench size={18} />
              </span>
              <ArrowRight size={16} style={{ color: "var(--text-muted)" }} />
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
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") navigate("/settings?tab=logs");
            }}
          >
            <div className="dashboard-action-header">
              <span className="dashboard-action-icon">
                <Terminal size={18} />
              </span>
              <ArrowRight size={16} style={{ color: "var(--text-muted)" }} />
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
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") navigate("/settings?tab=general");
            }}
          >
            <div className="dashboard-action-header">
              <span className="dashboard-action-icon">
                <Settings size={18} />
              </span>
              <ArrowRight size={16} style={{ color: "var(--text-muted)" }} />
            </div>
            <div>
              <h3 className="dashboard-action-title">{t("overview.action.settings")}</h3>
              <p className="dashboard-action-desc">{t("overview.action.settingsDesc")}</p>
            </div>
          </div>

          {/* DevTools Shortcut (visible if devMode enabled) */}
          {devMode && (
            <div
              className="dashboard-action-card"
              role="button"
              tabIndex={0}
              onClick={() => navigate("/settings?tab=devtools")}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") navigate("/settings?tab=devtools");
              }}
            >
              <div className="dashboard-action-header">
                <span className="dashboard-action-icon">
                  <Code2 size={18} />
                </span>
                <ArrowRight size={16} style={{ color: "var(--text-muted)" }} />
              </div>
              <div>
                <h3 className="dashboard-action-title">{t("settings.tabs.devtools")}</h3>
                <p className="dashboard-action-desc">{t("settings.devMode.desc")}</p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default OverviewView;
