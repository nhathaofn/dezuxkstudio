import React, { useEffect, useRef, useState } from "react";
import { useInRouterContext, useNavigate } from "react-router-dom";
import {
  Bot,
  Check,
  CheckCircle2,
  Copy,
  Download,
  FileAudio,
  Film,
  Image as ImageIcon,
  Loader2,
  Pause,
  Play,
  RotateCcw,
  Sparkles,
  Volume2,
  VolumeX,
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import type { ActionProgress } from "../../contracts/generated";
import { downloadMediaBlob, type ActionError } from "../../lib/rtClient";
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { formatByteSize } from "./types";

export type MediaResultKind = "image" | "video" | "tts";

export interface MediaOutputSummary {
  path: string;
  mimeType: string;
  provider: string;
  model: string;
  sizeBytes: number;
  duration?: number;
  asset_id?: string;
}

function formatAudioTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const mins = Math.floor(seconds / 60);
  const secs = Math.floor(seconds % 60);
  return `${mins}:${secs < 10 ? "0" : ""}${secs}`;
}

const WAVEFORM_BAR_COUNT = 36;
const DEFAULT_BAR_HEIGHTS = Array.from({ length: WAVEFORM_BAR_COUNT }, (_, i) => {
  const norm = i / (WAVEFORM_BAR_COUNT - 1);
  const envelope = Math.sin(norm * Math.PI);
  const ripple = Math.sin(norm * Math.PI * 6) * 0.25;
  const height = Math.max(0.2, Math.min(1, envelope * 0.72 + ripple + 0.18));
  return Math.round(height * 100);
});

export const AudioWaveformPlayer: React.FC<{ src: string; duration?: number }> = ({
  src,
  duration: initialDuration,
}) => {
  const audioRef = useRef<HTMLAudioElement>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(initialDuration ?? 0);
  const [isMuted, setIsMuted] = useState(false);

  const togglePlay = () => {
    const el = audioRef.current;
    if (!el) return;
    if (isPlaying) {
      el.pause();
    } else {
      void el.play();
    }
  };

  const toggleMute = () => {
    const el = audioRef.current;
    if (!el) return;
    el.muted = !isMuted;
    setIsMuted(!isMuted);
  };

  const handleSeek = (index: number) => {
    const el = audioRef.current;
    if (!el || duration <= 0) return;
    const targetRatio = index / (WAVEFORM_BAR_COUNT - 1);
    const targetTime = targetRatio * duration;
    el.currentTime = targetTime;
    setCurrentTime(targetTime);
  };

  const handleRestart = () => {
    const el = audioRef.current;
    if (!el) return;
    el.currentTime = 0;
    setCurrentTime(0);
    void el.play();
  };

  const progressRatio = duration > 0 ? Math.min(1, currentTime / duration) : 0;
  const currentBarIndex = Math.floor(progressRatio * WAVEFORM_BAR_COUNT);

  return (
    <div
      className="mediagen-waveform-player"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-2)",
        padding: "var(--space-3)",
        background: "var(--bg-surface-elevated)",
        border: "var(--border-width) solid var(--border-default)",
        borderRadius: "var(--radius-md)",
        width: "100%",
      }}
    >
      <audio
        ref={audioRef}
        src={src}
        preload="metadata"
        onPlay={() => setIsPlaying(true)}
        onPause={() => setIsPlaying(false)}
        onEnded={() => {
          setIsPlaying(false);
          setCurrentTime(0);
        }}
        onTimeUpdate={() => {
          if (audioRef.current) {
            setCurrentTime(audioRef.current.currentTime);
          }
        }}
        onLoadedMetadata={() => {
          if (audioRef.current && Number.isFinite(audioRef.current.duration)) {
            setDuration(audioRef.current.duration);
          }
        }}
      />

      <div
        className="mediagen-waveform-bars"
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          height: "44px",
          gap: "2px",
          cursor: "pointer",
          padding: "4px 0",
        }}
        role="slider"
        aria-label="Audio scrubber"
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(currentTime)}
      >
        {DEFAULT_BAR_HEIGHTS.map((pctHeight, idx) => {
          const isPassed = idx <= currentBarIndex;
          return (
            <div
              key={idx}
              onClick={() => handleSeek(idx)}
              style={{
                flex: 1,
                height: `${pctHeight}%`,
                minHeight: "4px",
                background: isPassed ? "var(--btn-primary-bg)" : "var(--border-strong)",
                opacity: isPassed ? 1 : 0.45,
                borderRadius: "var(--radius-sm)",
                transition: "background var(--transition-fast), opacity var(--transition-fast)",
              }}
              title={formatAudioTime((idx / (WAVEFORM_BAR_COUNT - 1)) * duration)}
            />
          );
        })}
      </div>

      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          fontSize: "var(--text-xs)",
          color: "var(--text-secondary)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
          <Button
            type="button"
            size="sm"
            variant="secondary"
            iconOnly
            onClick={togglePlay}
            aria-label={isPlaying ? "Pause" : "Play"}
            title={isPlaying ? "Pause" : "Play"}
          >
            {isPlaying ? <Pause size={13} /> : <Play size={13} />}
          </Button>

          <Button
            type="button"
            size="sm"
            variant="ghost"
            iconOnly
            onClick={handleRestart}
            aria-label="Restart"
            title="Restart"
          >
            <RotateCcw size={12} />
          </Button>

          <span style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatAudioTime(currentTime)} / {formatAudioTime(duration)}
          </span>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-1)" }}>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            iconOnly
            onClick={toggleMute}
            aria-label={isMuted ? "Unmute" : "Mute"}
            title={isMuted ? "Unmute" : "Mute"}
          >
            {isMuted ? <VolumeX size={13} /> : <Volume2 size={13} />}
          </Button>
        </div>
      </div>
    </div>
  );
};

interface MediaResultCardProps {
  kind: MediaResultKind;
  loading: boolean;
  progress: ActionProgress | null;
  error: ActionError | null;
  result: MediaOutputSummary | null;
  downloadingPath: string | null;
  onDownload: () => void;
  onRetry?: () => void;
  onDismissError?: () => void;
}

const RouterNavigateBridge: React.FC<{
  navRef: React.MutableRefObject<((path: string, opts?: { state?: unknown }) => void) | null>;
}> = ({ navRef }) => {
  const navigate = useNavigate();
  useEffect(() => {
    navRef.current = navigate;
  }, [navigate, navRef]);
  return null;
};

export const MediaResultCard: React.FC<MediaResultCardProps> = ({
  kind,
  loading,
  progress,
  error,
  result,
  downloadingPath,
  onDownload,
  onRetry,
  onDismissError,
}) => {
  const t = useT();
  const inRouter = useInRouterContext();
  const navRef = useRef<((path: string, opts?: { state?: unknown }) => void) | null>(null);
  const [copied, setCopied] = useState(false);
  const [previewState, setPreviewState] = useState<{ path: string; url: string | null }>({
    path: "",
    url: null,
  });

  const resultPath = result?.path ?? "";
  const isFetchingPreview = Boolean(
    resultPath && previewState.path !== resultPath && typeof URL.createObjectURL === "function"
  );

  useEffect(() => {
    if (!resultPath || typeof URL.createObjectURL !== "function") return;
    let cancelled = false;
    let objectUrl: string | null = null;

    void downloadMediaBlob(resultPath)
      .then(({ blob }) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setPreviewState({ path: resultPath, url: objectUrl });
      })
      .catch(() => {
        if (!cancelled) {
          setPreviewState({ path: resultPath, url: null });
        }
      });

    return () => {
      cancelled = true;
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [resultPath]);

  const activePreviewUrl = resultPath && previewState.path === resultPath ? previewState.url : null;

  const KindIcon = kind === "video" ? Film : kind === "tts" ? FileAudio : ImageIcon;
  const pct =
    progress?.percent != null ? Math.max(0, Math.min(100, Math.round(progress.percent))) : 0;

  const handleCopyPath = async () => {
    if (!result?.path) return;
    try {
      await navigator.clipboard.writeText(result.path);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard may be blocked in unprivileged frames
    }
  };

  const handleSendToWatermark = () => {
    if (!result?.path) return;
    if (navRef.current) {
      navRef.current("/watermark", { state: { asset_id: result.asset_id, path: result.path } });
    }
  };

  const handleAskAgent = () => {
    if (!result) return;
    askAgentAboutMediaAsset({
      assetId: result.asset_id,
      filePath: result.path,
      mimeType: result.mimeType,
    });
  };

  return (
    <Card
      icon={KindIcon}
      title={t("mediagen.card.output")}
      className="card--flex-fill"
      actions={
        result ? (
          <Badge className="watermark-badge-connected">
            <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
            {formatByteSize(result.sizeBytes)}
          </Badge>
        ) : undefined
      }
    >
      {inRouter && <RouterNavigateBridge navRef={navRef} />}
      {error && (
        <div
          className="mediagen-error-box"
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "var(--space-2) var(--space-3)",
            background: "var(--alert-error-bg)",
            border: "var(--border-width) solid var(--alert-error-border)",
            borderRadius: "var(--radius-md)",
            marginBottom: "var(--space-3)",
            color: "var(--alert-error-text)",
          }}
        >
          <div style={{ flex: 1, minWidth: 0 }}>
            <ErrorText error={error} />
          </div>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "var(--space-1)",
              flexShrink: 0,
              marginLeft: "var(--space-2)",
            }}
          >
            {onRetry && !loading && (
              <Button
                type="button"
                size="sm"
                variant="secondary"
                onClick={onRetry}
                title={t("mediagen.action.retry")}
              >
                <RotateCcw size={12} />
                {t("mediagen.action.retry")}
              </Button>
            )}
            {onDismissError && (
              <Button
                type="button"
                size="sm"
                iconOnly
                onClick={onDismissError}
                title={t("tools.cancel" as MessageKey) || "Dismiss"}
                aria-label="Dismiss error"
              >
                <X size={12} />
              </Button>
            )}
          </div>
        </div>
      )}

      {!loading && !result && (
        <div className="empty-state">
          <KindIcon size={34} className="empty-state__icon" />
          <div className="empty-state__title">{t("mediagen.empty.title")}</div>
          <div className="empty-state__desc">{t(`mediagen.empty.${kind}`)}</div>
        </div>
      )}

      {loading && (
        <div className="empty-state">
          <Loader2 size={28} className="animate-spin mediagen-spinner-icon" />
          <div className="empty-state__title">
            {(progress && translateServerMessage(t, progress.msg_key, progress.msg_params)) ||
              progress?.message ||
              t(`mediagen.running.${kind}`)}
          </div>
          {progress && (
            <div
              className="progress watermark-progress"
              role="progressbar"
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress__bar" style={{ width: `${pct}%` }} />
            </div>
          )}
        </div>
      )}

      {result && !loading && (
        <div className="watermark-results-body">
          {(isFetchingPreview || activePreviewUrl) && (
            <div className="mediagen-preview-box">
              {isFetchingPreview && (
                <div className="mediagen-preview-loading">
                  <Loader2 size={20} className="animate-spin" />
                </div>
              )}
              {activePreviewUrl && kind === "image" && (
                <img src={activePreviewUrl} alt={result.model} className="mediagen-preview-img" />
              )}
              {activePreviewUrl && kind === "video" && (
                <video
                  src={activePreviewUrl}
                  controls
                  playsInline
                  className="mediagen-preview-video"
                />
              )}
              {activePreviewUrl && kind === "tts" && (
                <AudioWaveformPlayer
                  key={activePreviewUrl}
                  src={activePreviewUrl}
                  duration={result.duration}
                />
              )}
            </div>
          )}

          <div className="mediagen-meta-grid">
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.provider")}</span>
              <span className="mediagen-meta-value">{result.provider}</span>
            </div>
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.model")}</span>
              <span className="mediagen-meta-value">{result.model}</span>
            </div>
            <div className="mediagen-meta-item">
              <span className="mediagen-meta-label">{t("mediagen.meta.mime")}</span>
              <span className="mediagen-meta-value">{result.mimeType}</span>
            </div>
            {result.duration != null && result.duration > 0 && (
              <div className="mediagen-meta-item">
                <span className="mediagen-meta-label">{t("mediagen.meta.duration")}</span>
                <span className="mediagen-meta-value">{result.duration}s</span>
              </div>
            )}
          </div>

          <div className="watermark-file-row">
            <div className="watermark-file-meta">
              <div className="mediagen-meta-label">{t("watermark.result.path")}</div>
              <div className="watermark-file-path" title={result.path}>
                {result.path}
              </div>
            </div>
            <div className="watermark-file-actions">
              {kind === "image" && (
                <button
                  type="button"
                  className="watermark-icon-btn"
                  title={t("cross.sendToWatermark" as MessageKey) || "Send to Watermark"}
                  aria-label="Send to Watermark"
                  onClick={handleSendToWatermark}
                >
                  <Sparkles size={12} />
                </button>
              )}
              <button
                type="button"
                className="watermark-icon-btn"
                title={t("cross.askAgent" as MessageKey) || "Ask Agent about this file"}
                aria-label="Ask Agent about this file"
                onClick={handleAskAgent}
              >
                <Bot size={12} />
              </button>
              <button
                type="button"
                className="watermark-icon-btn"
                title={t("mediagen.action.copyPath")}
                aria-label={t("mediagen.action.copyPath")}
                onClick={handleCopyPath}
              >
                {copied ? <Check size={12} /> : <Copy size={12} />}
              </button>
              <button
                type="button"
                className="watermark-icon-btn"
                title={t("mediagen.action.download")}
                aria-label={t("mediagen.action.download")}
                onClick={onDownload}
                disabled={downloadingPath === result.path}
              >
                {downloadingPath === result.path ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Download size={12} />
                )}
              </button>
            </div>
          </div>

          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: "var(--space-2)",
              marginTop: "var(--space-3)",
            }}
          >
            {kind === "image" && (
              <Button size="sm" variant="secondary" onClick={handleSendToWatermark}>
                <Sparkles size={12} />
                {t("cross.sendToWatermark" as MessageKey) || "Send to Watermark"}
              </Button>
            )}
            <Button size="sm" variant="secondary" onClick={handleAskAgent}>
              <Bot size={12} />
              {t("cross.askAgent" as MessageKey) || "Ask Agent about this file"}
            </Button>
          </div>
        </div>
      )}
    </Card>
  );
};
