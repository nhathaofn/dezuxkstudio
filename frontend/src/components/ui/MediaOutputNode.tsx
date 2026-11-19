import React, { useEffect, useRef, useState } from "react";
import {
  Bot,
  Check,
  Copy,
  Download,
  Eye,
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
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { ErrorText } from "./ErrorText";
import type { ActionProgress } from "../../contracts/generated";
import { downloadMediaBlob, type ActionError } from "../../lib/rtClient";
import { translateServerMessage, useT, type MessageKey } from "../../lib/i18n";

export type MediaOutputKind = "image" | "video" | "audio" | "tts";

export interface MediaOutputItem {
  outputPath: string;
  outputUrl?: string | null;
  sourcePath?: string | null;
  sourceUrl?: string | null;
  prompt?: string | null;
  mimeType?: string;
  provider?: string;
  model?: string;
  sizeBytes?: number;
  duration?: number;
  dimensions?: { width: number; height: number };
  asset_id?: string;
}

export interface MediaOutputNodeProps {
  kind: MediaOutputKind;
  item: MediaOutputItem | null;
  loading?: boolean;
  progress?: ActionProgress | null;
  error?: ActionError | string | null;
  downloading?: boolean;
  onDownload?: () => void;
  onRetry?: () => void;
  onDismissError?: () => void;
  onSendToWatermark?: (path: string) => void;
  onAskAgent?: (meta: { assetId?: string; filePath: string; mimeType?: string }) => void;
  className?: string;
  hideEmptyState?: boolean;
}

function formatAudioTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const mins = Math.floor(seconds / 60);
  const secs = Math.floor(seconds % 60);
  return `${mins}:${secs < 10 ? "0" : ""}${secs}`;
}

const WAVEFORM_BAR_COUNT = 36;
const NEUTRAL_PROGRESS_BARS: readonly number[] = Array.from(
  { length: WAVEFORM_BAR_COUNT },
  () => 38
);

export const AudioWaveformPlayer: React.FC<{ src: string; duration?: number }> = ({
  src,
  duration: initialDuration,
}) => {
  const t = useT();
  const audioRef = useRef<HTMLAudioElement>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(initialDuration ?? 0);
  const [isMuted, setIsMuted] = useState(false);
  const [bars, setBars] = useState<readonly number[]>(NEUTRAL_PROGRESS_BARS);

  useEffect(() => {
    const el = audioRef.current;
    return () => {
      if (!el) return;
      try {
        el.pause();
        el.removeAttribute("src");
        el.load();
      } catch {
        // ignore media element cleanup error in non-DOM test environments
      }
    };
  }, []);

  useEffect(() => {
    const AudioCtx =
      typeof window !== "undefined"
        ? window.AudioContext ||
          (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
        : undefined;
    if (!src || !AudioCtx || typeof fetch !== "function") {
      setBars(NEUTRAL_PROGRESS_BARS);
      return;
    }
    const controller = new AbortController();
    let closed = false;
    void (async () => {
      try {
        const res = await fetch(src, { signal: controller.signal });
        if (!res.ok) return;
        const buf = await res.arrayBuffer();
        if (controller.signal.aborted || buf.byteLength === 0) return;
        const ctx = new AudioCtx();
        try {
          const decoded = await ctx.decodeAudioData(buf);
          if (controller.signal.aborted) return;
          const ch = decoded.getChannelData(0);
          const blockSize = Math.max(1, Math.floor(ch.length / WAVEFORM_BAR_COUNT));
          const sampled: number[] = [];
          let peakMax = 0.001;
          for (let i = 0; i < WAVEFORM_BAR_COUNT; i++) {
            const start = i * blockSize;
            const end = Math.min(ch.length, start + blockSize);
            let sumSq = 0;
            for (let j = start; j < end; j++) {
              sumSq += ch[j] * ch[j];
            }
            const rms = Math.sqrt(sumSq / Math.max(1, end - start));
            if (rms > peakMax) peakMax = rms;
            sampled.push(rms);
          }
          setBars(sampled.map((v) => Math.max(18, Math.min(100, Math.round((v / peakMax) * 100)))));
        } finally {
          if (!closed) {
            closed = true;
            void ctx.close().catch(() => {});
          }
        }
      } catch {
        // Fallback stays NEUTRAL_PROGRESS_BARS
      }
    })();
    return () => {
      controller.abort();
      setBars(NEUTRAL_PROGRESS_BARS);
    };
  }, [src]);

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

  const seekToTime = (targetSec: number) => {
    const el = audioRef.current;
    if (!el || duration <= 0) return;
    const clamped = Math.max(0, Math.min(duration, targetSec));
    el.currentTime = clamped;
    setCurrentTime(clamped);
  };

  const handleSeek = (index: number) => {
    if (duration <= 0) return;
    seekToTime((index / (WAVEFORM_BAR_COUNT - 1)) * duration);
  };

  const handleSliderKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (duration <= 0) return;
    const step = Math.max(1, duration / 20);
    switch (e.key) {
      case "ArrowRight":
      case "ArrowUp":
        e.preventDefault();
        seekToTime(currentTime + step);
        break;
      case "ArrowLeft":
      case "ArrowDown":
        e.preventDefault();
        seekToTime(currentTime - step);
        break;
      case "Home":
        e.preventDefault();
        seekToTime(0);
        break;
      case "End":
        e.preventDefault();
        seekToTime(duration);
        break;
    }
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
        padding: "var(--space-2) var(--space-3)",
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
          height: "36px",
          gap: "2px",
          cursor: "pointer",
          padding: "2px 0",
        }}
        role="slider"
        tabIndex={0}
        aria-label={t("mediaoutput.audioScrubber")}
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(currentTime)}
        onKeyDown={handleSliderKeyDown}
      >
        {bars.map((pctHeight, idx) => {
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
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={togglePlay}
            aria-label={isPlaying ? t("mediaoutput.pause") : t("mediaoutput.play")}
            title={isPlaying ? t("mediaoutput.pause") : t("mediaoutput.play")}
          >
            {isPlaying ? <Pause size={13} /> : <Play size={13} />}
          </button>

          <button
            type="button"
            className="watermark-icon-btn"
            onClick={handleRestart}
            aria-label={t("mediaoutput.restart")}
            title={t("mediaoutput.restart")}
          >
            <RotateCcw size={12} />
          </button>

          <span style={{ fontVariantNumeric: "tabular-nums" }}>
            {formatAudioTime(currentTime)} / {formatAudioTime(duration)}
          </span>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-1)" }}>
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={toggleMute}
            aria-label={isMuted ? t("mediaoutput.unmute") : t("mediaoutput.mute")}
            title={isMuted ? t("mediaoutput.unmute") : t("mediaoutput.mute")}
          >
            {isMuted ? <VolumeX size={13} /> : <Volume2 size={13} />}
          </button>
        </div>
      </div>
    </div>
  );
};

function formatByteSize(bytes?: number): string {
  if (!bytes || !Number.isFinite(bytes) || bytes <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let val = bytes;
  let idx = 0;
  while (val >= 1024 && idx < units.length - 1) {
    val /= 1024;
    idx++;
  }
  return `${val >= 10 || idx === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[idx]}`;
}

export const MediaOutputNode: React.FC<MediaOutputNodeProps> = ({
  kind,
  item,
  loading = false,
  progress = null,
  error = null,
  downloading = false,
  onDownload,
  onRetry,
  onDismissError,
  onSendToWatermark,
  onAskAgent,
  className = "",
  hideEmptyState = false,
}) => {
  const t = useT();

  const [copiedPath, setCopiedPath] = useState(false);
  const [copiedPrompt, setCopiedPrompt] = useState(false);
  const [isComparing, setIsComparing] = useState(false);
  const [compareZoom, setCompareZoom] = useState(1.0);
  const [compareSlider, setCompareSlider] = useState(50);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const copyPathTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const copyPromptTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    const vid = videoRef.current;
    return () => {
      if (copyPathTimerRef.current !== null) clearTimeout(copyPathTimerRef.current);
      if (copyPromptTimerRef.current !== null) clearTimeout(copyPromptTimerRef.current);
      if (!vid) return;
      try {
        vid.pause();
        vid.removeAttribute("src");
        vid.load();
      } catch {
        // ignore in test DOM
      }
    };
  }, []);

  const [retryNonce, setRetryNonce] = useState(0);
  const [outputState, setOutputState] = useState<{
    key: string;
    url: string | null;
    error: string | null;
  }>({
    key: "",
    url: null,
    error: null,
  });
  const [resolvedSourceUrl, setResolvedSourceUrl] = useState<string | null>(null);

  const outputPath = item?.outputPath ?? "";
  const explicitOutputUrl = item?.outputUrl ?? null;
  const sourcePath = item?.sourcePath ?? "";
  const explicitSourceUrl = item?.sourceUrl ?? null;

  const hasOutputState = outputState.key === `${outputPath}#${retryNonce}`;
  const resolvedOutputUrl = hasOutputState ? outputState.url : null;
  const previewError = hasOutputState ? outputState.error : null;
  const previewLoading =
    !explicitOutputUrl &&
    Boolean(outputPath) &&
    (!hasOutputState || (!resolvedOutputUrl && !previewError));

  // Resolve object URL for output media when needed (async download)
  useEffect(() => {
    if (explicitOutputUrl || !outputPath || typeof URL.createObjectURL !== "function") {
      return;
    }

    let cancelled = false;
    let objUrl: string | null = null;
    const currentKey = `${outputPath}#${retryNonce}`;

    const loadOutput = async () => {
      try {
        let res: { blob: Blob };
        try {
          res = await downloadMediaBlob(outputPath, null, item?.asset_id);
        } catch (err) {
          if (item?.asset_id) {
            res = await downloadMediaBlob(outputPath, null);
          } else {
            throw err;
          }
        }
        if (cancelled) return;
        objUrl = URL.createObjectURL(res.blob);
        setOutputState({ key: currentKey, url: objUrl, error: null });
      } catch (err) {
        if (cancelled) return;
        setOutputState({
          key: currentKey,
          url: null,
          error: err instanceof Error ? err.message : String(err),
        });
      }
    };

    void loadOutput();

    return () => {
      cancelled = true;
      if (objUrl) {
        try {
          URL.revokeObjectURL(objUrl);
        } catch {
          // ignore
        }
      }
    };
  }, [outputPath, explicitOutputUrl, item?.asset_id, retryNonce]);

  // Resolve object URL for source media (for comparison) when needed (async download)
  useEffect(() => {
    if (explicitSourceUrl || !sourcePath || typeof URL.createObjectURL !== "function") {
      return;
    }

    let cancelled = false;
    let objUrl: string | null = null;

    void downloadMediaBlob(sourcePath)
      .then(({ blob }) => {
        if (cancelled) return;
        objUrl = URL.createObjectURL(blob);
        setResolvedSourceUrl(objUrl);
      })
      .catch(() => {
        if (!cancelled) setResolvedSourceUrl(null);
      });

    return () => {
      cancelled = true;
      if (objUrl) {
        try {
          URL.revokeObjectURL(objUrl);
        } catch {
          // ignore
        }
      }
      setResolvedSourceUrl(null);
    };
  }, [sourcePath, explicitSourceUrl]);

  const activeOutputUrl = explicitOutputUrl || resolvedOutputUrl;
  const activeSourceUrl = explicitSourceUrl || resolvedSourceUrl;
  const hasSource = Boolean(sourcePath || explicitSourceUrl);
  const isImage = kind === "image";
  const isVideo = kind === "video";
  const isAudio = kind === "audio" || kind === "tts";

  const handleCopyPath = async () => {
    if (!item?.outputPath) return;
    try {
      await navigator.clipboard.writeText(item.outputPath);
      setCopiedPath(true);
      if (copyPathTimerRef.current !== null) clearTimeout(copyPathTimerRef.current);
      copyPathTimerRef.current = setTimeout(() => {
        copyPathTimerRef.current = null;
        setCopiedPath(false);
      }, 1800);
    } catch {
      // ignore
    }
  };

  const handleCopyPrompt = async () => {
    if (!item?.prompt) return;
    try {
      await navigator.clipboard.writeText(item.prompt);
      setCopiedPrompt(true);
      if (copyPromptTimerRef.current !== null) clearTimeout(copyPromptTimerRef.current);
      copyPromptTimerRef.current = setTimeout(() => {
        copyPromptTimerRef.current = null;
        setCopiedPrompt(false);
      }, 1800);
    } catch {
      // ignore
    }
  };

  const KindIcon = isVideo ? Film : isAudio ? FileAudio : ImageIcon;
  const pct =
    progress?.percent != null ? Math.max(0, Math.min(100, Math.round(progress.percent))) : 0;

  const runningKey: MessageKey =
    kind === "audio" ? "mediagen.running.tts" : `mediagen.running.${kind}`;
  const emptyKey: MessageKey = kind === "audio" ? "mediagen.empty.tts" : `mediagen.empty.${kind}`;

  return (
    <div className={`media-output-node ${className}`}>
      {/* 1. Error Banner */}
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
            marginBottom: "var(--space-2)",
            color: "var(--alert-error-text)",
          }}
        >
          <div style={{ flex: 1, minWidth: 0 }}>
            {typeof error === "string" ? error : <ErrorText error={error} />}
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
              <button
                type="button"
                className="watermark-icon-btn"
                onClick={onRetry}
                title={t("mediaoutput.retry")}
                aria-label={t("mediaoutput.retry")}
              >
                <RotateCcw size={12} />
              </button>
            )}
            {onDismissError && (
              <button
                type="button"
                className="watermark-icon-btn"
                onClick={onDismissError}
                title={t("mediaoutput.dismissError")}
                aria-label={t("mediaoutput.dismissError")}
              >
                <X size={12} />
              </button>
            )}
          </div>
        </div>
      )}

      {/* 2. Loading State */}
      {loading && (
        <div className="empty-state">
          <Loader2 size={26} className="animate-spin mediagen-spinner-icon" />
          <div className="empty-state__title">
            {(progress && translateServerMessage(t, progress.msg_key, progress.msg_params)) ||
              progress?.message ||
              t(runningKey)}
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

      {/* 3. Empty State */}
      {!loading && !item && !hideEmptyState && (
        <div className="empty-state">
          <KindIcon size={32} className="empty-state__icon" />
          <div className="empty-state__title">{t("mediagen.empty.title")}</div>
          <div className="empty-state__desc">{t(emptyKey)}</div>
        </div>
      )}

      {/* 4. Result Ready Content */}
      {item && !loading && (
        <div className="watermark-results-body" style={{ width: "100%" }}>
          {/* Prompt strip (compact 1-row with hover tooltip and copy button) */}
          {item.prompt && (
            <div className="media-output-prompt-strip">
              <span className="media-output-prompt-text" title={item.prompt}>
                "{item.prompt}"
              </span>
              <button
                type="button"
                className="watermark-icon-btn"
                title={copiedPrompt ? t("mediaoutput.copiedPrompt") : t("mediaoutput.copyPrompt")}
                aria-label={t("mediaoutput.copyPrompt")}
                onClick={handleCopyPrompt}
              >
                {copiedPrompt ? <Check size={12} /> : <Copy size={12} />}
              </button>
            </div>
          )}

          {/* Media Viewport */}
          <div className="media-output-viewport">
            {!activeOutputUrl && previewLoading && (
              <div className="mediagen-preview-loading">
                <Loader2 size={24} className="animate-spin mediagen-spinner-icon" />
              </div>
            )}

            {!activeOutputUrl && !previewLoading && previewError && (
              <div className="mediagen-preview-fallback">
                <div className="mediagen-preview-fallback-inner">
                  <KindIcon size={28} className="muted-text" />
                  <span className="mediagen-preview-error-title">
                    {t("mediaoutput.previewFailed")}
                  </span>
                  <button
                    type="button"
                    className="ds-button ds-button--sm ds-button--secondary"
                    onClick={() => {
                      setRetryNonce((n) => n + 1);
                    }}
                  >
                    <RotateCcw size={12} /> {t("mediaoutput.retry")}
                  </button>
                </div>
              </div>
            )}

            {/* Video Viewport */}
            {activeOutputUrl && isVideo && (
              <video
                ref={videoRef}
                src={activeOutputUrl}
                controls
                playsInline
                preload="metadata"
                className="media-output-preview-video"
              />
            )}

            {/* Audio Viewport */}
            {activeOutputUrl && isAudio && (
              <AudioWaveformPlayer
                key={activeOutputUrl}
                src={activeOutputUrl}
                duration={item.duration}
              />
            )}

            {/* Image Viewport: Normal vs Comparison Mode */}
            {activeOutputUrl && isImage && !isComparing && (
              <img
                src={activeOutputUrl}
                alt={item.model || t("mediaoutput.altOutput")}
                className="media-output-preview-img"
              />
            )}

            {/* Image Viewport: Comparison Mode */}
            {activeOutputUrl && isImage && isComparing && (
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: "var(--space-2)",
                  width: "100%",
                }}
              >
                {/* Comparison Control Toolbar */}
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: "var(--space-2)",
                    fontSize: "var(--text-xs)",
                  }}
                >
                  <div style={{ display: "flex", alignItems: "center", gap: "var(--space-1)" }}>
                    <button
                      type="button"
                      className="watermark-icon-btn"
                      onClick={() => setCompareZoom((z) => Math.max(0.5, +(z - 0.25).toFixed(2)))}
                      disabled={compareZoom <= 0.5}
                      title={t("mediaoutput.zoomOut")}
                      aria-label={t("mediaoutput.zoomOut")}
                    >
                      <ZoomOut size={12} />
                    </button>
                    <span
                      style={{
                        minWidth: "40px",
                        textAlign: "center",
                        fontFamily: "var(--font-mono)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      {Math.round(compareZoom * 100)}%
                    </span>
                    <button
                      type="button"
                      className="watermark-icon-btn"
                      onClick={() => setCompareZoom((z) => Math.min(3.0, +(z + 0.25).toFixed(2)))}
                      disabled={compareZoom >= 3.0}
                      title={t("mediaoutput.zoomIn")}
                      aria-label={t("mediaoutput.zoomIn")}
                    >
                      <ZoomIn size={12} />
                    </button>
                    <button
                      type="button"
                      className="watermark-icon-btn"
                      onClick={() => setCompareZoom(1.0)}
                      title={t("mediaoutput.zoomReset")}
                      aria-label={t("mediaoutput.zoomReset")}
                    >
                      <RotateCcw size={11} />
                    </button>
                  </div>

                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "var(--space-2)",
                      color: "var(--text-muted)",
                    }}
                  >
                    <span>{t("mediaoutput.before")}</span>
                    <input
                      type="range"
                      min={0}
                      max={100}
                      value={compareSlider}
                      onChange={(e) => setCompareSlider(Number(e.target.value))}
                      aria-label={t("mediaoutput.splitSlider")}
                      style={{ width: "90px", cursor: "pointer" }}
                    />
                    <span>{t("mediaoutput.after")}</span>
                  </div>
                </div>

                {/* Comparison Image Container */}
                <div
                  style={{
                    position: "relative",
                    width: "100%",
                    height: "230px",
                    overflow: "hidden",
                    borderRadius: "var(--radius-sm)",
                    background: "var(--bg-canvas)",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                  }}
                >
                  <div
                    style={{
                      transform: `scale(${compareZoom})`,
                      transformOrigin: "center center",
                      position: "relative",
                      display: "inline-block",
                      transition: "transform var(--transition-fast)",
                    }}
                  >
                    {/* After image (base layer) */}
                    <img
                      src={activeOutputUrl}
                      alt={t("mediaoutput.altAfter")}
                      style={{ maxHeight: "210px", maxWidth: "100%", display: "block" }}
                    />

                    {/* Before image (clipped overlay) */}
                    {activeSourceUrl && (
                      <div
                        style={{
                          position: "absolute",
                          top: 0,
                          left: 0,
                          width: "100%",
                          height: "100%",
                          overflow: "hidden",
                          clipPath: `inset(0 ${100 - compareSlider}% 0 0)`,
                          pointerEvents: "none",
                        }}
                      >
                        <img
                          src={activeSourceUrl}
                          alt={t("mediaoutput.altBefore")}
                          style={{ maxHeight: "210px", maxWidth: "100%", display: "block" }}
                        />
                      </div>
                    )}

                    {/* Split line */}
                    <div
                      style={{
                        position: "absolute",
                        top: 0,
                        bottom: 0,
                        left: `${compareSlider}%`,
                        width: "2px",
                        backgroundColor: "var(--accent)",
                        pointerEvents: "none",
                      }}
                    />
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Metadata & Actions row (Icon-Only buttons, Monochromatic, with Hover Tooltip) */}
          <div className="media-output-footer-row">
            {/* Metadata Tags */}
            <div className="media-output-meta-strip">
              {item.model && (
                <span
                  className="media-output-meta-badge"
                  title={item.provider ? `${item.provider} • ${item.model}` : item.model}
                >
                  {item.model}
                </span>
              )}
              {item.mimeType && <span className="media-output-meta-item">{item.mimeType}</span>}
              {item.sizeBytes != null && item.sizeBytes > 0 && (
                <span className="media-output-meta-item">{formatByteSize(item.sizeBytes)}</span>
              )}
              {item.duration != null && item.duration > 0 && (
                <span className="media-output-meta-item">{item.duration.toFixed(1)}s</span>
              )}
            </div>

            {/* Monochromatic Icon-Only Action Buttons */}
            <div className="media-output-actions">
              {/* Compare toggle (only if source is available) */}
              {hasSource && isImage && (
                <button
                  type="button"
                  className={`watermark-icon-btn ${isComparing ? "watermark-icon-btn--active" : ""}`}
                  title={t(isComparing ? "mediaoutput.closeCompare" : "mediaoutput.compare")}
                  aria-label={t(isComparing ? "mediaoutput.closeCompare" : "mediaoutput.compare")}
                  onClick={() => setIsComparing((c) => !c)}
                >
                  <Eye size={13} />
                </button>
              )}

              {/* Send to Watermark (images only) */}
              {isImage && onSendToWatermark && (
                <button
                  type="button"
                  className="watermark-icon-btn"
                  title={t("mediaoutput.sendToWatermark")}
                  aria-label={t("mediaoutput.sendToWatermark")}
                  onClick={() => onSendToWatermark(item.outputPath)}
                >
                  <Sparkles size={13} />
                </button>
              )}

              {/* Ask AI Agent */}
              {onAskAgent && (
                <button
                  type="button"
                  className="watermark-icon-btn"
                  title={t("mediaoutput.askAgent")}
                  aria-label={t("mediaoutput.askAgent")}
                  onClick={() =>
                    onAskAgent({
                      assetId: item.asset_id,
                      filePath: item.outputPath,
                      mimeType: item.mimeType,
                    })
                  }
                >
                  <Bot size={13} />
                </button>
              )}

              {/* Copy Path */}
              <button
                type="button"
                className="watermark-icon-btn"
                title={copiedPath ? t("mediaoutput.copiedPath") : t("mediaoutput.copyPath")}
                aria-label={t("mediaoutput.copyPath")}
                onClick={handleCopyPath}
              >
                {copiedPath ? <Check size={13} /> : <Copy size={13} />}
              </button>

              {/* Download */}
              {onDownload && (
                <button
                  type="button"
                  className="watermark-icon-btn"
                  title={t("mediaoutput.download")}
                  aria-label={t("mediaoutput.download")}
                  onClick={onDownload}
                  disabled={downloading}
                >
                  {downloading ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <Download size={13} />
                  )}
                </button>
              )}
            </div>
          </div>

          {/* Destination Path row */}
          <div className="media-output-path-row">
            <span className="media-output-path-label">{t("watermark.result.path")}:</span>
            <span className="media-output-path-value" title={item.outputPath}>
              {item.outputPath}
            </span>
          </div>
        </div>
      )}
    </div>
  );
};

export default MediaOutputNode;
