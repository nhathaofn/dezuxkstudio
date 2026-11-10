import React, { useState } from "react";
import { Layers, FileImage, Loader2, CheckCircle2, Check, AlertCircle, Copy } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { useT } from "../../lib/i18n";
import { useWatermarkStore } from "./useWatermarkStore";

export const WatermarkResultsCard: React.FC = () => {
  const t = useT();
  const [copiedPath, setCopiedPath] = useState<string | null>(null);

  const { inputPath, inputPaths, loading, result } = useWatermarkStore();

  const handleCopy = (text: string) => {
    void navigator.clipboard.writeText(text);
    setCopiedPath(text);
    setTimeout(() => setCopiedPath(null), 2000);
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
          <div className="empty-state__title">{t("watermark.processing")}</div>
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

          {/* Single File Result Display */}
          {(!result.results || result.results.length <= 1) && result.image && (
            <div className="watermark-file-row">
              <div className="watermark-file-meta">
                <div className="muted-text">{t("watermark.result.path")}</div>
                <div className="watermark-file-path" title={result.image.localUrl}>
                  {result.image.localUrl}
                </div>
              </div>

              <Button
                size="sm"
                iconOnly
                onClick={() => handleCopy(result.image.localUrl)}
                title={t("watermark.copyPath")}
                aria-label={t("watermark.copyPath")}
              >
                {copiedPath === result.image.localUrl ? <Check size={12} /> : <Copy size={12} />}
              </Button>
            </div>
          )}

          {/* Multiple Items Results List */}
          {result.results && result.results.length > 1 && (
            <div className="watermark-scroll-list">
              {result.results.map((item, idx) => (
                <div key={`${item.path}-${idx}`} className="watermark-file-row">
                  <div className="watermark-file-meta">
                    <div className="watermark-file-path" title={item.path}>
                      {item.path}
                    </div>
                    {item.out_path && item.out_path !== item.path && (
                      <div className="watermark-file-subpath" title={item.out_path}>
                        → {item.out_path}
                      </div>
                    )}
                    {item.error && <div className="watermark-file-error">{item.error}</div>}
                  </div>

                  <div className="watermark-file-actions">
                    {item.error ? (
                      <Badge className="watermark-badge-error">
                        <AlertCircle size={11} className="watermark-badge-icon" />{" "}
                        {t("watermark.result.error")}
                      </Badge>
                    ) : item.detected ? (
                      <Badge className="watermark-badge-connected">
                        <CheckCircle2 size={11} className="watermark-badge-icon" />{" "}
                        {t("watermark.result.detectedScore", {
                          score: Math.round(item.score * 100),
                        })}
                      </Badge>
                    ) : (
                      <Badge>
                        <Check size={11} className="watermark-badge-icon" />{" "}
                        {t("watermark.result.cleanBadge")}
                      </Badge>
                    )}

                    {item.out_path && (
                      <button
                        type="button"
                        onClick={() => handleCopy(item.out_path)}
                        className="watermark-icon-btn"
                        title={t("watermark.copyPath")}
                        aria-label={t("watermark.copyPath")}
                      >
                        {copiedPath === item.out_path ? <Check size={12} /> : <Copy size={12} />}
                      </button>
                    )}
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
