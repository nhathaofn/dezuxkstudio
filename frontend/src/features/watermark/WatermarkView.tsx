import React from "react";
import { Sparkles, Loader2, RotateCcw } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { useWatermarkStore, type WatermarkNodeResult } from "./useWatermarkStore";
import { WatermarkConfigCard } from "./WatermarkConfigCard";
import { WatermarkResultsCard } from "./WatermarkResultsCard";

export const WatermarkView: React.FC = () => {
  const t = useT();

  const { inputPath, inputPaths, loading, result, error, removeWatermark, reset } =
    useWatermarkStore();

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;
  const { inPlace, outputDir, workers, corner } = useWatermarkStore();

  // Let the agent see this screen: loaded images, current settings (keyed by the node's param names), last run.
  useAgentScreenContext({
    feature: "tool.watermark_remover",
    selected_files: inputPaths.length > 0 ? inputPaths : inputPath ? [inputPath] : [],
    settings: {
      corner,
      workers: String(workers),
      inplace: String(inPlace),
      output_dir: outputDir,
    },
    last_result: loading ? "running…" : error ? `error: ${error.message}` : summarizeResult(result),
  });

  return (
    <div className="dashboard-layout watermark-layout">
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

      <ErrorText error={error} />

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
