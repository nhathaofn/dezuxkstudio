import React from "react";
import { Sparkles, Loader2, RotateCcw } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { ErrorText } from "../../components/ui/ErrorText";
import { useT } from "../../lib/i18n";
import { useWatermarkStore } from "./useWatermarkStore";
import { WatermarkConfigCard } from "./WatermarkConfigCard";
import { WatermarkResultsCard } from "./WatermarkResultsCard";

export const WatermarkView: React.FC = () => {
  const t = useT();

  const { inputPath, inputPaths, loading, result, error, removeWatermark, reset } =
    useWatermarkStore();

  const totalFiles = inputPaths.length > 0 ? inputPaths.length : inputPath ? 1 : 0;

  return (
    <div className="dashboard-layout" style={{ maxWidth: 1280 }}>
      {/* Header Bar */}
      <div className="dashboard-hero" style={{ padding: "var(--space-3) var(--space-5)" }}>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
          <h1 className="dashboard-hero-title" style={{ margin: 0, fontSize: "var(--text-xl)" }}>
            {t("watermark.title")}
          </h1>
          <div className="dashboard-hero-meta" style={{ gap: "var(--space-2)" }}>
            <Badge>{t("watermark.badge.model")}</Badge>
            <Badge>{t("watermark.badge.multiThreaded")}</Badge>
          </div>
        </div>

        {/* Global Action Bar */}
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
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

      {/* Main 2-Column Balanced Grid */}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "360px 1fr",
          gap: "var(--space-4)",
          alignItems: "start",
        }}
      >
        <WatermarkConfigCard />
        <WatermarkResultsCard />
      </div>
    </div>
  );
};

export default WatermarkView;
