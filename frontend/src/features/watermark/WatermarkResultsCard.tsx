import { appConfig } from "../../config/appConfig";
import React, { useEffect, useState } from "react";
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
  X,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { isMessageKey, translateServerMessage, useT } from "../../lib/i18n";
import { downloadMediaBlob, mediaBasename } from "../../lib/rtClient";
import { useConnectionStore } from "../connection/useConnectionStore";
import { useWatermarkStore, type WatermarkItemResult } from "./useWatermarkStore";

function usePreviewUrl(path: string): string | null {
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!path || typeof URL.createObjectURL !== "function") return;
    let cancelled = false;
    let objUrl: string | null = null;
    void downloadMediaBlob(path)
      .then(({ blob }) => {
        if (cancelled) return;
        objUrl = URL.createObjectURL(blob);
        setUrl(objUrl);
      })
      .catch(() => {
        if (!cancelled) setUrl(null);
      });
    return () => {
      cancelled = true;
      if (objUrl) URL.revokeObjectURL(objUrl);
    };
  }, [path]);
  return url;
}

const ComparisonViewer: React.FC<{
  beforePath: string;
  afterPath: string;
  title: string;
  onClose: () => void;
}> = ({ beforePath, afterPath, title, onClose }) => {
  const {
    comparisonZoom,
    comparisonPan,
    comparisonSliderPos,
    setComparisonZoom,
    setComparisonPan,
    setComparisonSliderPos,
  } = useWatermarkStore();

  const [isPanning, setIsPanning] = useState(false);
  const [dragStart, setDragStart] = useState<{ x: number; y: number } | null>(null);

  const beforeUrl = usePreviewUrl(beforePath);
  const afterUrl = usePreviewUrl(afterPath);

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
          aria-label="Close comparison"
          title="Close comparison"
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
            aria-label="Zoom out"
            title="Zoom out"
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
            aria-label="Zoom in"
            title="Zoom in"
          >
            <ZoomIn size={13} />
          </button>
          <button
            type="button"
            className="watermark-icon-btn"
            onClick={handleZoomReset}
            aria-label="Reset zoom and pan"
            title="Reset zoom"
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
          <span className="muted-text">Before</span>
          <input
            type="range"
            min={0}
            max={100}
            value={comparisonSliderPos}
            onChange={(e) => setComparisonSliderPos(Number(e.target.value))}
            aria-label="Before/After split slider"
            style={{ width: "120px", cursor: "pointer" }}
          />
          <span className="muted-text">After</span>
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
              alt="After cleaned"
              style={{
                maxHeight: "240px",
                maxWidth: "100%",
                display: "block",
                pointerEvents: "none",
              }}
            />
          ) : (
            <div style={{ padding: "var(--space-4)", textAlign: "center", fontSize: "var(--text-xs)" }}>
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
                alt="Before original"
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
    remoteNameMap,
    loading,
    downloadingPath,
    progress,
    result,
    comparisonItem,
    downloadOutput,
    setComparisonItem,
    resetComparison,
  } = useWatermarkStore();

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
    const status =
      item.status || (item.error ? "failed" : item.detected ? "processed" : "skipped");
    if (status === "failed") {
      return (
        <Badge className="watermark-badge-error" title={item.error}>
          <AlertCircle size={11} className="watermark-badge-icon" />{" "}
          {t("watermark.result.error")}
        </Badge>
      );
    }
    if (status === "processed") {
      return (
        <Badge className="watermark-badge-connected">
          <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
          {item.score != null && item.score > 0
            ? t("watermark.result.detectedScore", { score: Math.round(item.score * 100) })
            : isMessageKey("watermark.status.processed")
              ? t("watermark.status.processed")
              : "Processed"}
        </Badge>
      );
    }
    if (status === "copied") {
      return (
        <Badge className="watermark-badge-copied">
          <Copy size={11} className="watermark-badge-icon" />{" "}
          {isMessageKey("watermark.status.copied") ? t("watermark.status.copied") : "Copied"}
        </Badge>
      );
    }
    return (
      <Badge className="watermark-badge-skipped" title={item.reason || t("watermark.result.cleanBadge")}>
        <Check size={11} className="watermark-badge-icon" />{" "}
        {item.status === "skipped" && isMessageKey("watermark.status.skipped")
          ? t("watermark.status.skipped")
          : item.status === "skipped"
            ? "Skipped"
            : t("watermark.result.cleanBadge")}
      </Badge>
    );
  };

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;
  const currentFilesList = inputPaths.length > 0 ? inputPaths : inputPath ? [inputPath] : [];

  const totalProcessed = result?.total_count ?? (result ? 1 : 0);
  const detectedCount = result?.detected_count ?? (result?.detected ? 1 : 0);
  const cleanCount = Math.max(0, totalProcessed - detectedCount);

  return (
    <Card
      icon={Layers}
      title={t("watermark.card.output")}
      className="card--flex-fill"
      actions={
        result ? (
          <Badge className="watermark-badge-connected">
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
                <Badge>{t("watermark.status.ready")}</Badge>
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

            <div className="watermark-kpi-card watermark-kpi-card--accent">
              <div className="watermark-kpi-label">{t("watermark.stat.detected")}</div>
              <div className="watermark-kpi-value">{detectedCount}</div>
            </div>

            <div className="watermark-kpi-card">
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
            <div className="watermark-file-row">
              <div className="watermark-file-meta">
                <div className="muted-text">{t("watermark.result.path")}</div>
                <div className="watermark-file-path" title={result.image.localUrl}>
                  {displayName(result.image.localUrl)}
                </div>
              </div>

              <div className="watermark-file-actions">
                {result.results && result.results[0] && renderItemStatusBadge(result.results[0])}

                {inputPath && result.image.localUrl && (
                  <button
                    type="button"
                    onClick={() =>
                      setComparisonItem({
                        beforeUrl: inputPath,
                        afterUrl: result.image.localUrl,
                        title: displayName(result.image.localUrl),
                      })
                    }
                    className="watermark-icon-btn"
                    title="Compare Before / After"
                    aria-label="Compare Before / After"
                  >
                    <Eye size={12} />
                  </button>
                )}

                {isRemoteMode ? (
                  <Button
                    size="sm"
                    iconOnly
                    disabled={downloadingPath === result.image.localUrl}
                    onClick={() =>
                      void downloadOutput(result.image.localUrl, mediaBasename(result.image.localUrl))
                    }
                    title={t("watermark.download")}
                    aria-label={t("watermark.download")}
                  >
                    {downloadingPath === result.image.localUrl ? (
                      <Loader2 size={12} className="animate-spin" />
                    ) : (
                      <Download size={12} />
                    )}
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    iconOnly
                    onClick={() => handleCopy(result.image.localUrl)}
                    title={t("watermark.copyPath")}
                    aria-label={t("watermark.copyPath")}
                  >
                    {copiedPath === result.image.localUrl ? <Check size={12} /> : <Copy size={12} />}
                  </Button>
                )}
              </div>
            </div>
          )}

          {/* Multiple Items Results List */}
          {result.results && result.results.length > 1 && (
            <div className="watermark-scroll-list">
              {result.results.map((item, idx) => (
                <div key={`${item.path}-${idx}`} className="watermark-file-row">
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
                        title="Compare Before / After"
                        aria-label="Compare Before / After"
                      >
                        <Eye size={12} />
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
              ))}
            </div>
          )}
        </div>
      )}
    </Card>
  );
};

export default WatermarkResultsCard;
