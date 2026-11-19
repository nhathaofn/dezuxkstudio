import { appConfig } from "../../config/appConfig";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { useFlash } from "../../lib/hooks/useFlash";
import {
  Layers,
  FileImage,
  Loader2,
  CheckCircle2,
  Check,
  AlertCircle,
  Copy,
  Download,
  Eye,
  ZoomIn,
  ZoomOut,
  RotateCcw,
  Sparkles,
  Bot,
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { MediaOutputNode } from "../../components/ui/MediaOutputNode";
import { VirtualList } from "../../components/ui/VirtualList";
import { translateServerMessage, useT } from "../../lib/i18n";
import { downloadMediaBlob, mediaBasename } from "../../lib/rtClient";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useWatermarkStore } from "./useWatermarkStore";

function safeRevokeObjectURL(url: string | null | undefined): void {
  if (!url || typeof URL === "undefined" || typeof URL.revokeObjectURL !== "function") return;
  try {
    URL.revokeObjectURL(url);
  } catch (_err) {
    void _err;
  }
}

interface PreviewState {
  url: string | null;
  loading: boolean;
  error: boolean;
  retry: () => void;
}

function usePreviewUrl(path: string): PreviewState {
  const [url, setUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(Boolean(path));
  const [error, setError] = useState<boolean>(false);
  const [attempt, setAttempt] = useState(0);

  const retry = useCallback(() => {
    setAttempt((n) => n + 1);
  }, []);

  useEffect(() => {
    if (!path || typeof URL === "undefined" || typeof URL.createObjectURL !== "function") {
      setLoading(false);
      setError(false);
      setUrl(null);
      return;
    }
    let cancelled = false;
    let activeObjUrl: string | null = null;
    setLoading(true);
    setError(false);
    void downloadMediaBlob(path)
      .then(({ blob }) => {
        if (cancelled) return;
        activeObjUrl = URL.createObjectURL(blob);
        setUrl(activeObjUrl);
        setLoading(false);
      })
      .catch(() => {
        if (!cancelled) {
          setUrl(null);
          setLoading(false);
          setError(true);
        }
      });
    return () => {
      cancelled = true;
      if (activeObjUrl) {
        safeRevokeObjectURL(activeObjUrl);
        activeObjUrl = null;
      }
      setUrl(null);
    };
  }, [path, attempt]);
  return { url, loading, error, retry };
}

const ComparisonViewer: React.FC<{
  beforePath: string;
  afterPath: string;
  title: string;
  onClose: () => void;
}> = ({ beforePath, afterPath, title, onClose }) => {
  const t = useT();
  const {
    comparisonZoom,
    comparisonPan,
    comparisonSliderPos,
    setComparisonZoom,
    setComparisonPan,
    setComparisonSliderPos,
  } = useWatermarkStore(
    useShallow((s) => ({
      comparisonZoom: s.comparisonZoom,
      comparisonPan: s.comparisonPan,
      comparisonSliderPos: s.comparisonSliderPos,
      setComparisonZoom: s.setComparisonZoom,
      setComparisonPan: s.setComparisonPan,
      setComparisonSliderPos: s.setComparisonSliderPos,
    }))
  );

  const [isPanning, setIsPanning] = useState(false);
  const [dragStart, setDragStart] = useState<{ x: number; y: number } | null>(null);

  const beforeState = usePreviewUrl(beforePath);
  const afterState = usePreviewUrl(afterPath);
  const beforeUrl = beforeState.url;
  const afterUrl = afterState.url;

  const { zoomMin, zoomMax, zoomStep } = appConfig.ui.watermarkCompare;

  const handleZoomIn = () => {
    setComparisonZoom((z) => Math.min(zoomMax, +(z + zoomStep).toFixed(2)));
  };

  const handleZoomOut = () => {
    setComparisonZoom((z) => Math.max(zoomMin, +(z - zoomStep).toFixed(2)));
  };

  const handleZoomReset = () => {
    setComparisonZoom(1.0);
    setComparisonPan({ x: 0, y: 0 });
  };

  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button !== 0) return;
    setIsPanning(true);
    setDragStart({ x: e.clientX - comparisonPan.x, y: e.clientY - comparisonPan.y });
  };

  const handleMouseMove = (e: React.MouseEvent) => {
    if (!isPanning || !dragStart) return;
    setComparisonPan({
      x: Math.round(e.clientX - dragStart.x),
      y: Math.round(e.clientY - dragStart.y),
    });
  };

  const handleMouseUp = () => {
    setIsPanning(false);
    setDragStart(null);
  };

  return (
    <div
      className="watermark-comparison-viewer"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-2)",
        padding: "var(--space-3)",
        border: "var(--border-width) solid var(--border-subtle)",
        borderRadius: "var(--radius-md)",
        background: "var(--bg-surface-elevated)",
      }}
    >
      {/* Header with Title and Close Button */}
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
        <span
          className="watermark-file-path"
          style={{ fontWeight: "var(--weight-semibold)", fontSize: "var(--text-sm)" }}
          title={title}
        >
          {title}
        </span>
        <button
          type="button"
          onClick={onClose}
          className="watermark-icon-btn"
          aria-label={t("watermark.comparison.close")}
          title={t("watermark.comparison.close")}
        >
          <X size={14} />
        </button>
      </div>

      {/* Zoom and Pan Toolbar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: "var(--space-2)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-1)" }}>
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={handleZoomOut}
            disabled={comparisonZoom <= zoomMin}
            aria-label={t("watermark.comparison.zoomOut")}
            title={t("watermark.comparison.zoomOut")}
          >
            <ZoomOut size={13} />
          </button>
          <span
            style={{
              fontSize: "var(--text-xs)",
              minWidth: "48px",
              textAlign: "center",
              fontFamily: "var(--font-mono)",
            }}
          >
            {Math.round(comparisonZoom * 100)}%
          </span>
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={handleZoomIn}
            disabled={comparisonZoom >= zoomMax}
            aria-label={t("watermark.comparison.zoomIn")}
            title={t("watermark.comparison.zoomIn")}
          >
            <ZoomIn size={13} />
          </button>
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={handleZoomReset}
            aria-label={t("watermark.comparison.zoomReset")}
            title={t("watermark.comparison.zoomReset")}
          >
            <RotateCcw size={12} />
          </button>
        </div>

        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: "var(--space-2)",
            fontSize: "var(--text-xs)",
          }}
        >
          <span className="muted-text">{t("watermark.comparison.before")}</span>
          <input
            type="range"
            min={0}
            max={100}
            value={comparisonSliderPos}
            onChange={(e) => setComparisonSliderPos(Number(e.target.value))}
            aria-label={t("watermark.comparison.slider")}
            style={{ width: "120px", cursor: "pointer" }}
          />
          <span className="muted-text">{t("watermark.comparison.after")}</span>
        </div>
      </div>

      {/* Interactive Image Viewport */}
      <div
        onMouseDown={handleMouseDown}
        onMouseMove={handleMouseMove}
        onMouseUp={handleMouseUp}
        onMouseLeave={handleMouseUp}
        style={{
          position: "relative",
          width: "100%",
          height: "260px",
          overflow: "hidden",
          borderRadius: "var(--radius-sm)",
          background: "var(--bg-canvas)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: isPanning ? "grabbing" : "grab",
          userSelect: "none",
        }}
      >
        <div
          style={{
            transform: `scale(${comparisonZoom}) translate(${comparisonPan.x}px, ${comparisonPan.y}px)`,
            transformOrigin: "center center",
            position: "relative",
            display: "inline-block",
            transition: isPanning ? "none" : "transform var(--transition-fast)",
          }}
        >
          {/* After image (base layer) */}
          {afterUrl ? (
            <img
              src={afterUrl}
              alt={t("watermark.comparison.after")}
              style={{
                maxHeight: "240px",
                maxWidth: "100%",
                display: "block",
                pointerEvents: "none",
              }}
            />
          ) : afterState.error ? (
            <div
              style={{
                padding: "var(--space-4)",
                textAlign: "center",
                fontSize: "var(--text-xs)",
                display: "flex",
                flexDirection: "column",
                alignItems: "center",
                gap: "var(--space-2)",
              }}
            >
              <AlertCircle size={18} style={{ color: "var(--danger)" }} />
              <span className="muted-text">{t("watermark.preview.error")}</span>
              <Button
                size="sm"
                variant="secondary"
                onClick={() => {
                  afterState.retry();
                  beforeState.retry();
                }}
              >
                {t("watermark.preview.retry")}
              </Button>
            </div>
          ) : (
            <div
              style={{ padding: "var(--space-4)", textAlign: "center", fontSize: "var(--text-xs)" }}
            >
              <Loader2 size={16} className="animate-spin" />
            </div>
          )}

          {/* Before image (clipped overlay) */}
          {beforeUrl && (
            <div
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                height: "100%",
                overflow: "hidden",
                clipPath: `inset(0 ${100 - comparisonSliderPos}% 0 0)`,
                pointerEvents: "none",
              }}
            >
              <img
                src={beforeUrl}
                alt={t("watermark.comparison.before")}
                style={{
                  maxHeight: "240px",
                  maxWidth: "100%",
                  display: "block",
                }}
              />
            </div>
          )}

          {/* Split divider line */}
          <div
            style={{
              position: "absolute",
              top: 0,
              bottom: 0,
              left: `${comparisonSliderPos}%`,
              width: "2px",
              backgroundColor: "var(--accent)",
              pointerEvents: "none",
              boxShadow: "0 0 4px var(--overlay-bg)",
            }}
          />
        </div>
      </div>
    </div>
  );
};

export const WatermarkResultsCard: React.FC = () => {
  const t = useT();
  const [copiedPath, flashCopiedPath] = useFlash<string>(appConfig.ui.copyFeedbackMs);
  const endpointMode = useConnectionStore((s) => s.endpoint?.mode);
  const isRemoteMode = endpointMode === "browser" || endpointMode === "client";

  const {
    inputPath,
    inputPaths,
    queuedFiles,
    remoteNameMap,
    loading,
    downloadingPath,
    progress,
    result,
    comparisonItem,
    downloadOutput,
    setComparisonItem,
    resetComparison,
    setInputPath,
    setInputPaths,
    removeQueuedFile,
  } = useWatermarkStore(
    useShallow((s) => ({
      inputPath: s.inputPath,
      inputPaths: s.inputPaths,
      queuedFiles: s.queuedFiles,
      remoteNameMap: s.remoteNameMap,
      loading: s.loading,
      downloadingPath: s.downloadingPath,
      progress: s.progress,
      result: s.result,
      comparisonItem: s.comparisonItem,
      downloadOutput: s.downloadOutput,
      setComparisonItem: s.setComparisonItem,
      resetComparison: s.resetComparison,
      setInputPath: s.setInputPath,
      setInputPaths: s.setInputPaths,
      removeQueuedFile: s.removeQueuedFile,
    }))
  );

  const ephemeralUrlsRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    const urls = ephemeralUrlsRef.current;
    return () => {
      urls.forEach((u) => {
        safeRevokeObjectURL(u);
      });
      urls.clear();
    };
  }, []);

  const displayName = (rawPath: string) =>
    remoteNameMap[rawPath] || (isRemoteMode ? mediaBasename(rawPath) : rawPath);

  const handleCopy = (text: string) => {
    void navigator.clipboard.writeText(text);
    flashCopiedPath(text);
  };

  const renderItemStatusBadge = (item: {
    status?: string;
    detected?: boolean;
    score?: number;
    error?: string;
    reason?: string;
  }) => {
    const status = item.status || (item.error ? "failed" : item.detected ? "processed" : "skipped");
    if (status === "failed") {
      return (
        <Badge className="watermark-badge-error" title={item.error}>
          <AlertCircle size={11} className="watermark-badge-icon" /> {t("watermark.result.error")}
        </Badge>
      );
    }
    if (status === "processed") {
      return (
        <Badge className="watermark-badge-connected">
          <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
          {item.score != null && item.score > 0
            ? t("watermark.result.detectedScore", { score: Math.round(item.score * 100) })
            : t("watermark.status.processed")}
        </Badge>
      );
    }
    if (status === "copied") {
      return (
        <Badge className="watermark-badge-copied">
          <Copy size={11} className="watermark-badge-icon" /> {t("watermark.status.copied")}
        </Badge>
      );
    }
    return (
      <Badge
        className="watermark-badge-skipped"
        title={item.reason || t("watermark.result.cleanBadge")}
      >
        <Check size={11} className="watermark-badge-icon" />{" "}
        {item.status === "skipped"
          ? t("watermark.status.skipped")
          : t("watermark.result.cleanBadge")}
      </Badge>
    );
  };

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;
  const currentFilesList = inputPaths.length > 0 ? inputPaths : inputPath ? [inputPath] : [];

  const totalProcessed = result?.total_count ?? (result ? 1 : 0);
  const detectedCount = result?.detected_count ?? (result?.detected ? 1 : 0);
  const failedCount = result?.results?.filter((r) => Boolean(r.error)).length ?? 0;
  const cleanCount = Math.max(0, totalProcessed - detectedCount - failedCount);

  return (
    <Card
      icon={Layers}
      title={t("watermark.card.output")}
      className="card--flex-fill"
      actions={
        result ? (
          <Badge className={detectedCount > 0 ? "watermark-badge-connected" : ""}>
            {t("watermark.stat.detected")}: {detectedCount}/{totalProcessed}
          </Badge>
        ) : totalFiles > 0 ? (
          <Badge>{t("watermark.badge.filesCount", { count: totalFiles })}</Badge>
        ) : null
      }
    >
      {/* State 1: Nothing selected yet */}
      {totalFiles === 0 && !result && (
        <div className="empty-state">
          <FileImage size={34} className="empty-state__icon" />
          <div className="empty-state__title">{t("watermark.emptyQueue")}</div>
          <div className="empty-state__desc">{t("watermark.emptyQueueDesc")}</div>
        </div>
      )}

      {/* State 2: Files queued, not yet processed */}
      {totalFiles > 0 && !result && !loading && (
        <div className="watermark-results-body">
          <div className="muted-text">{t("watermark.readyFiles", { count: totalFiles })}</div>
          <div className="watermark-scroll-list">
            {currentFilesList.map((p, idx) => (
              <div key={`${p}-${idx}`} className="watermark-file-row">
                <span className="watermark-file-path" title={p}>
                  {p}
                </span>
                <div className="watermark-file-actions">
                  <Badge>{t("watermark.status.ready")}</Badge>
                  <button
                    type="button"
                    className="watermark-icon-btn"
                    title={t("watermark.removeFile")}
                    aria-label={t("watermark.removeFile")}
                    onClick={() => {
                      if (queuedFiles.length > 0) {
                        removeQueuedFile(idx);
                      } else if (inputPaths.length > 0) {
                        setInputPaths(inputPaths.filter((_, i) => i !== idx));
                      } else {
                        setInputPath("");
                      }
                    }}
                  >
                    <X size={12} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* State 3: Processing loading */}
      {loading && (
        <div className="empty-state">
          <Loader2 size={28} className="animate-spin" style={{ color: "var(--accent)" }} />
          <div className="empty-state__title">
            {(progress && translateServerMessage(t, progress.msg_key, progress.msg_params)) ||
              progress?.message ||
              t("watermark.processing")}
          </div>
          {progress && (
            <div
              className="progress watermark-progress"
              role="progressbar"
              aria-valuenow={Math.round(progress.percent)}
              aria-valuemin={0}
              aria-valuemax={100}
            >
              <div className="progress__bar" style={{ width: `${progress.percent}%` }} />
            </div>
          )}
        </div>
      )}

      {/* State 4: Results ready */}
      {result && !loading && (
        <div className="watermark-results-body">
          {/* Mini KPI Metric Stats */}
          <div className="watermark-kpi-grid">
            <div className="watermark-kpi-card">
              <div className="watermark-kpi-label">{t("watermark.stat.total")}</div>
              <div className="watermark-kpi-value">{totalProcessed}</div>
            </div>

            <div
              className={`watermark-kpi-card ${
                detectedCount > 0 ? "watermark-kpi-card--accent" : ""
              }`}
            >
              <div className="watermark-kpi-label">{t("watermark.stat.detected")}</div>
              <div className="watermark-kpi-value">{detectedCount}</div>
            </div>

            <div
              className={`watermark-kpi-card ${cleanCount > 0 ? "watermark-kpi-card--clean" : ""}`}
            >
              <div className="watermark-kpi-label">{t("watermark.stat.clean")}</div>
              <div className="watermark-kpi-value">{cleanCount}</div>
            </div>
          </div>

          {/* Before/After Visual Comparison Slider (when an item is selected for comparison) */}
          {comparisonItem && (
            <ComparisonViewer
              beforePath={comparisonItem.beforeUrl}
              afterPath={comparisonItem.afterUrl}
              title={comparisonItem.title}
              onClose={resetComparison}
            />
          )}

          {/* Single File Result Display */}
          {(!result.results || result.results.length <= 1) && result.image && (
            <MediaOutputNode
              kind="image"
              item={{
                outputPath: result.image.localUrl,
                sourcePath: inputPath || undefined,
                mimeType: result.image.kind,
                asset_id: result.results?.[0]?.asset_id,
              }}
              downloading={downloadingPath === result.image.localUrl}
              onDownload={
                isRemoteMode
                  ? () =>
                      void downloadOutput(
                        result.image.localUrl,
                        mediaBasename(result.image.localUrl)
                      )
                  : undefined
              }
              onSendToWatermark={() => setInputPath(result.image.localUrl)}
              onAskAgent={(meta) =>
                askAgentAboutMediaAsset({
                  assetId: meta.assetId,
                  filePath: meta.filePath,
                })
              }
            />
          )}

          {/* Multiple Items Results List */}
          {result.results && result.results.length > 1 && (
            <VirtualList
              items={result.results}
              className="watermark-scroll-list"
              style={{ display: "block" }}
              height={Math.min(380, Math.max(160, result.results.length * 52))}
              itemHeight={(_idx, item) =>
                item.error ? 68 : item.out_path && item.out_path !== item.path ? 54 : 48
              }
              getItemKey={(item, idx) => `${item.path}-${idx}`}
              renderItem={(item) => (
                <div className="watermark-file-row">
                  <div className="watermark-file-meta">
                    <div className="watermark-file-path" title={item.path}>
                      {displayName(item.path)}
                    </div>
                    {item.out_path && item.out_path !== item.path && (
                      <div className="watermark-file-subpath" title={item.out_path}>
                        → {displayName(item.out_path)}
                      </div>
                    )}
                    {item.error && <div className="watermark-file-error">{item.error}</div>}
                  </div>

                  <div className="watermark-file-actions">
                    {renderItemStatusBadge(item)}

                    {item.out_path && (
                      <button
                        type="button"
                        onClick={() =>
                          setComparisonItem({
                            beforeUrl: item.path,
                            afterUrl: item.out_path,
                            title: displayName(item.path),
                          })
                        }
                        className="watermark-icon-btn"
                        title={t("watermark.compare")}
                        aria-label={t("watermark.compare")}
                      >
                        <Eye size={12} />
                      </button>
                    )}

                    {item.out_path && (
                      <button
                        type="button"
                        onClick={() => setInputPath(item.out_path)}
                        className="watermark-icon-btn"
                        title={t("cross.sendToWatermark")}
                        aria-label={t("cross.sendToWatermark")}
                      >
                        <Sparkles size={12} />
                      </button>
                    )}

                    {item.out_path && (
                      <button
                        type="button"
                        onClick={() =>
                          askAgentAboutMediaAsset({
                            assetId: item.asset_id,
                            filePath: item.out_path,
                          })
                        }
                        className="watermark-icon-btn"
                        title={t("cross.askAgent")}
                        aria-label={t("cross.askAgent")}
                      >
                        <Bot size={12} />
                      </button>
                    )}

                    {item.out_path &&
                      (isRemoteMode ? (
                        <button
                          type="button"
                          disabled={downloadingPath === item.out_path}
                          onClick={() =>
                            void downloadOutput(item.out_path, mediaBasename(item.out_path))
                          }
                          className="watermark-icon-btn"
                          title={t("watermark.download")}
                          aria-label={t("watermark.download")}
                        >
                          {downloadingPath === item.out_path ? (
                            <Loader2 size={12} className="animate-spin" />
                          ) : (
                            <Download size={12} />
                          )}
                        </button>
                      ) : (
                        <button
                          type="button"
                          onClick={() => handleCopy(item.out_path)}
                          className="watermark-icon-btn"
                          title={t("watermark.copyPath")}
                          aria-label={t("watermark.copyPath")}
                        >
                          {copiedPath === item.out_path ? <Check size={12} /> : <Copy size={12} />}
                        </button>
                      ))}
                  </div>
                </div>
              )}
            />
          )}
        </div>
      )}
    </Card>
  );
};

export default WatermarkResultsCard;
