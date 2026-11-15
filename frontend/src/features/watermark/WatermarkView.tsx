import React, { useEffect } from "react";
import { useInRouterContext, useLocation } from "react-router-dom";
import { Sparkles, Loader2, RotateCcw, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { MediaDropZone } from "../../components/ui/MediaDropZone";
import { describeError, useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { WATERMARK_NODE, useWatermarkStore, type WatermarkNodeResult } from "./useWatermarkStore";
import { WatermarkConfigCard } from "./WatermarkConfigCard";
import { WatermarkResultsCard } from "./WatermarkResultsCard";

/**
 * Handles incoming navigation parameters (e.g. 1-click cross action from MediaGen or MediaLibrary).
 * Safely isolated to avoid router context errors in standalone test harnesses.
 */
const WatermarkNavigationListener: React.FC = () => {
  const location = useLocation();
  const setInputPath = useWatermarkStore((s) => s.setInputPath);

  useEffect(() => {
    const state = location.state as { asset_id?: string; path?: string } | null;
    const searchParams = new URLSearchParams(location.search);
    const incomingAssetId = state?.asset_id || searchParams.get("asset_id");
    const incomingPath = state?.path || searchParams.get("path");
    const target = incomingPath || incomingAssetId;
    if (target) {
      setInputPath(target);
    }
  }, [location.state, location.search, setInputPath]);

  return null;
};

export const WatermarkView: React.FC = () => {
  const t = useT();
  const inRouter = useInRouterContext();

  const {
    inputPath,
    inputPaths,
    loading,
    result,
    error,
    setInputPaths,
    setQueuedFiles,
    removeWatermark,
    reset,
    dismissError,
  } = useWatermarkStore();

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;
  const { inPlace, outputDir, workers, corner } = useWatermarkStore();

  const handleDropZoneSelected = (files: File[], paths?: string[]) => {
    if (paths && paths.length > 0) {
      setInputPaths(paths);
    } else if (files.length > 0) {
      setQueuedFiles(files);
    }
  };

  // Let the agent see this screen: loaded images, current settings (keyed by the node's param names), last run.
  useAgentScreenContext({
    feature: WATERMARK_NODE,
    selected_files: inputPaths.length > 0 ? inputPaths : inputPath ? [inputPath] : [],
    // Only what the user changed; unset settings fall back to the node's defaults.
    settings: {
      ...(corner !== null ? { corner } : {}),
      ...(workers !== null ? { workers: String(workers) } : {}),
      inplace: String(inPlace),
      output_dir: outputDir,
    },
    last_result: loading ? "running…" : error ? `error: ${error.message}` : summarizeResult(result),
  });

  return (
    <div className="dashboard-layout watermark-layout">
      {inRouter && <WatermarkNavigationListener />}

      {/* Header Bar */}
      <div className="dashboard-hero watermark-hero">
        <div className="watermark-hero-title-group">
          <h1 className="dashboard-hero-title watermark-hero-title">{t("watermark.title")}</h1>
          <div className="dashboard-hero-meta">
            <Badge>{t("watermark.badge.model")}</Badge>
            <Badge>{t("watermark.badge.multiThreaded")}</Badge>
          </div>
        </div>

        {/* Global Action Bar */}
        <div className="watermark-hero-actions">
          {(totalFiles > 0 || result) && (
            <Button size="sm" onClick={reset} disabled={loading}>
              <RotateCcw size={13} />
              {t("watermark.resetBtn")}
            </Button>
          )}

          <Button
            variant="primary"
            size="sm"
            disabled={totalFiles === 0 || loading}
            onClick={() => void removeWatermark()}
          >
            {loading ? (
              <>
                <Loader2 size={13} className="animate-spin" />
                {t("watermark.processing")}
              </>
            ) : (
              <>
                <Sparkles size={13} />
                {t("watermark.startBtn")}
              </>
            )}
          </Button>
        </div>
      </div>

      {error && (
        <div
          className="alert-box"
          role="alert"
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "var(--space-2)",
          }}
        >
          <div style={{ flex: 1 }}>{describeError(t, error)}</div>
          <button
            type="button"
            onClick={dismissError}
            aria-label="Dismiss error"
            title="Dismiss error"
            className="watermark-icon-btn"
            style={{ flexShrink: 0 }}
          >
            <X size={14} />
          </button>
        </div>
      )}

      {/* Shared Dropzone for fast onboarding when no files are loaded */}
      {totalFiles === 0 && !result && (
        <div style={{ marginBottom: "var(--space-4)" }}>
          <MediaDropZone
            multiple
            label={t("watermark.browse.title")}
            title={t("watermark.dragOrClick")}
            description={t("watermark.dragOrClickDesc")}
            onSelected={handleDropZoneSelected}
          />
        </div>
      )}

      {/* Main 2-Column Responsive Workspace */}
      <div className="watermark-grid">
        <WatermarkConfigCard />
        <WatermarkResultsCard />
      </div>
    </div>
  );
};

export default WatermarkView;

function summarizeResult(result: WatermarkNodeResult | null): string {
  if (!result) return "";
  const items = result.results ?? [];
  const failed = items.filter((r) => r.error).length;
  const outputs = items.filter((r) => r.detected && r.out_path).map((r) => r.out_path);
  const parts = [
    `${result.detected_count ?? 0}/${result.total_count ?? items.length} images had a watermark and were cleaned`,
  ];
  if (failed > 0) parts.push(`${failed} failed`);
  if (outputs.length > 0) {
    parts.push(`outputs: ${outputs.slice(0, 5).join(", ")}${outputs.length > 5 ? ", …" : ""}`);
  }
  return parts.join("; ");
}
