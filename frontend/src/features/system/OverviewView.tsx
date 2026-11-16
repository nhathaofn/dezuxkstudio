import React from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowRight,
  Clock,
  Film,
  Image as ImageIcon,
  Settings,
  Sliders,
  Sparkles,
  Terminal,
  Volume2,
  Wrench,
} from "lucide-react";
import { useConnectionStore } from "../connection/useConnectionStore";
import { SystemCard } from "./SystemCard";
import { UpdateCard } from "./UpdateCard";
import { useSystemStore } from "./useSystemStore";
import { useT } from "../../lib/i18n";
import { formatDuration } from "../../lib/format";

export const OverviewView: React.FC = () => {
  const t = useT();
  const navigate = useNavigate();

  const info = useSystemStore((s) => s.info);
  const status = useConnectionStore((s) => s.status);

  const formatUptime = (sec: number): string => formatDuration(t, sec);

  const handleShortcutKeyDown = (e: React.KeyboardEvent, to: string) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      navigate(to);
    }
  };

  return (
    <div className="dashboard-layout">
      {/* Modern Welcome & Live Status Hero */}
      <header className="dashboard-hero">
        <div className="dashboard-hero-main">
          <h1 className="dashboard-hero-title">
            <span>{t("overview.welcome")}</span>
          </h1>
          <p className="dashboard-hero-subtitle">{t("overview.subtitle")}</p>
        </div>
        <div className="hero-telemetry-group">
          <div className="hero-status-pill" aria-live="polite">
            <span className={`status-dot status-dot--${status}`} />
            <span>{t(`status.${status}`)}</span>
          </div>
          {info?.build && (
            <span className="hero-meta-pill" title="Version">
              v{info.build.version}
            </span>
          )}
          {info?.build && (
            <span className="hero-meta-pill" title="Platform">
              {info.build.os}/{info.build.arch}
            </span>
          )}
          {info?.runtime && (
            <span className="hero-meta-pill" title={t("system.uptime")}>
              <Clock size={12} />
              {formatUptime(info.runtime.uptime_seconds)}
            </span>
          )}
        </div>
      </header>

      {/* Main Balanced Bento Layout */}
      <div className="overview-bento">
        {/* Left: Organized Action Shortcuts */}
        <section className="overview-shortcuts-section" aria-label={t("overview.quickActions")}>
          {/* Group 1: Media & AI Studio Features */}
          <div className="overview-section">
            <div className="overview-section-header">
              <h2 className="overview-section-title">
                <Sparkles size={14} />
                {t("overview.studioSection")}
              </h2>
            </div>

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
                  <span className="dashboard-action-icon dashboard-action-icon--watermark">
                    <Sparkles size={19} />
                  </span>
                  <div className="dashboard-action-header-right">
                    <span className="dashboard-action-tag">{t("overview.tag.watermark")}</span>
                    <ArrowRight size={16} className="dashboard-action-arrow" />
                  </div>
                </div>
                <div className="dashboard-action-body">
                  <h3 className="dashboard-action-title">{t("overview.action.watermark")}</h3>
                  <p className="dashboard-action-desc">{t("overview.action.watermarkDesc")}</p>
                </div>
              </div>

              {/* Image Generation Shortcut */}
              <div
                className="dashboard-action-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/image")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/image")}
              >
                <div className="dashboard-action-header">
                  <span className="dashboard-action-icon dashboard-action-icon--image">
                    <ImageIcon size={19} />
                  </span>
                  <div className="dashboard-action-header-right">
                    <span className="dashboard-action-tag">{t("overview.tag.image")}</span>
                    <ArrowRight size={16} className="dashboard-action-arrow" />
                  </div>
                </div>
                <div className="dashboard-action-body">
                  <h3 className="dashboard-action-title">{t("overview.action.image")}</h3>
                  <p className="dashboard-action-desc">{t("overview.action.imageDesc")}</p>
                </div>
              </div>

              {/* Video Generation Shortcut */}
              <div
                className="dashboard-action-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/video")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/video")}
              >
                <div className="dashboard-action-header">
                  <span className="dashboard-action-icon dashboard-action-icon--video">
                    <Film size={19} />
                  </span>
                  <div className="dashboard-action-header-right">
                    <span className="dashboard-action-tag">{t("overview.tag.video")}</span>
                    <ArrowRight size={16} className="dashboard-action-arrow" />
                  </div>
                </div>
                <div className="dashboard-action-body">
                  <h3 className="dashboard-action-title">{t("overview.action.video")}</h3>
                  <p className="dashboard-action-desc">{t("overview.action.videoDesc")}</p>
                </div>
              </div>

              {/* TTS Generation Shortcut */}
              <div
                className="dashboard-action-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/tts")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/tts")}
              >
                <div className="dashboard-action-header">
                  <span className="dashboard-action-icon dashboard-action-icon--tts">
                    <Volume2 size={19} />
                  </span>
                  <div className="dashboard-action-header-right">
                    <span className="dashboard-action-tag">{t("overview.tag.tts")}</span>
                    <ArrowRight size={16} className="dashboard-action-arrow" />
                  </div>
                </div>
                <div className="dashboard-action-body">
                  <h3 className="dashboard-action-title">{t("overview.action.tts")}</h3>
                  <p className="dashboard-action-desc">{t("overview.action.ttsDesc")}</p>
                </div>
              </div>
            </div>
          </div>

          {/* Group 2: System Utilities & Preferences */}
          <div className="overview-section">
            <div className="overview-section-header">
              <h2 className="overview-section-title">
                <Sliders size={14} />
                {t("overview.systemSection")}
              </h2>
            </div>

            <div className="dashboard-utilities-grid">
              {/* Tools Shortcut */}
              <div
                className="dashboard-utility-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/settings?tab=tools")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=tools")}
              >
                <div className="dashboard-utility-top">
                  <span className="dashboard-utility-icon dashboard-utility-icon--tools">
                    <Wrench size={16} />
                  </span>
                  <ArrowRight size={15} className="dashboard-utility-arrow" />
                </div>
                <div className="dashboard-utility-body">
                  <h3 className="dashboard-utility-title">{t("overview.action.tools")}</h3>
                  <p className="dashboard-utility-desc">{t("overview.action.toolsDesc")}</p>
                </div>
              </div>

              {/* Logs Shortcut */}
              <div
                className="dashboard-utility-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/settings?tab=logs")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=logs")}
              >
                <div className="dashboard-utility-top">
                  <span className="dashboard-utility-icon dashboard-utility-icon--logs">
                    <Terminal size={16} />
                  </span>
                  <ArrowRight size={15} className="dashboard-utility-arrow" />
                </div>
                <div className="dashboard-utility-body">
                  <h3 className="dashboard-utility-title">{t("overview.action.logs")}</h3>
                  <p className="dashboard-utility-desc">{t("overview.action.logsDesc")}</p>
                </div>
              </div>

              {/* Settings Shortcut */}
              <div
                className="dashboard-utility-card"
                role="button"
                tabIndex={0}
                onClick={() => navigate("/settings?tab=general")}
                onKeyDown={(e) => handleShortcutKeyDown(e, "/settings?tab=general")}
              >
                <div className="dashboard-utility-top">
                  <span className="dashboard-utility-icon dashboard-utility-icon--settings">
                    <Settings size={16} />
                  </span>
                  <ArrowRight size={15} className="dashboard-utility-arrow" />
                </div>
                <div className="dashboard-utility-body">
                  <h3 className="dashboard-utility-title">{t("overview.action.settings")}</h3>
                  <p className="dashboard-utility-desc">{t("overview.action.settingsDesc")}</p>
                </div>
              </div>
            </div>
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
