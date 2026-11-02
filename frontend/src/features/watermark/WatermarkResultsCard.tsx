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
      actions={
        result ? (
          <Badge
            style={{
              backgroundColor: "var(--status-connected-bg)",
              color: "var(--status-connected)",
              fontWeight: "var(--weight-semibold)",
            }}
          >
            {t("watermark.stat.detected")}: {detectedCount}/{totalProcessed}
          </Badge>
        ) : totalFiles > 0 ? (
          <Badge>{t("watermark.badge.filesCount", { count: totalFiles })}</Badge>
        ) : null
      }
    >
      {/* State 1: Nothing selected yet */}
      {totalFiles === 0 && !result && (
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            padding: "var(--space-8) var(--space-4)",
            color: "var(--text-muted)",
            textAlign: "center",
          }}
        >
          <FileImage size={36} style={{ marginBottom: "var(--space-2)", opacity: 0.3 }} />
          <div style={{ fontSize: "var(--text-sm)", fontWeight: "var(--weight-medium)" }}>
            {t("watermark.emptyQueue")}
          </div>
          <div style={{ fontSize: "var(--text-xs)", color: "var(--text-secondary)", marginTop: 2 }}>
            {t("watermark.emptyQueueDesc")}
          </div>
        </div>
      )}

      {/* State 2: Files queued, not yet processed */}
      {totalFiles > 0 && !result && !loading && (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-2)" }}>
          <div
            style={{
              fontSize: "var(--text-xs)",
              color: "var(--text-muted)",
              marginBottom: "var(--space-1)",
            }}
          >
            {t("watermark.readyFiles", { count: totalFiles })}
          </div>
          <div
            style={{
              maxHeight: 320,
              overflowY: "auto",
              display: "flex",
              flexDirection: "column",
              gap: "var(--space-1)",
            }}
          >
            {currentFilesList.map((p, idx) => (
              <div
                key={idx}
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "var(--space-2) var(--space-3)",
                  borderRadius: "var(--radius-sm)",
                  backgroundColor: "var(--bg-canvas)",
                  border: "1px solid var(--border-subtle)",
                  fontSize: "var(--text-xs)",
                }}
              >
                <span
                  style={{
                    fontFamily: "var(--font-mono)",
                    color: "var(--text-primary)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    marginRight: "var(--space-2)",
                  }}
                >
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
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            alignItems: "center",
            justifyContent: "center",
            padding: "var(--space-8) var(--space-4)",
            gap: "var(--space-2)",
          }}
        >
          <Loader2 size={28} className="animate-spin" style={{ color: "var(--text-accent)" }} />
          <div style={{ fontSize: "var(--text-sm)", fontWeight: "var(--weight-medium)" }}>
            {t("watermark.processing")}
          </div>
        </div>
      )}

      {/* State 4: Results ready */}
      {result && !loading && (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
          {/* Mini KPI Metric Stats */}
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(3, 1fr)",
              gap: "var(--space-2)",
            }}
          >
            <div
              style={{
                padding: "var(--space-2) var(--space-3)",
                borderRadius: "var(--radius-sm)",
                backgroundColor: "var(--bg-canvas)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <div style={{ fontSize: "var(--text-xs)", color: "var(--text-muted)" }}>
                {t("watermark.stat.total")}
              </div>
              <div style={{ fontSize: "var(--text-lg)", fontWeight: "var(--weight-bold)" }}>
                {totalProcessed}
              </div>
            </div>

            <div
              style={{
                padding: "var(--space-2) var(--space-3)",
                borderRadius: "var(--radius-sm)",
                backgroundColor: "var(--status-connected-bg)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <div style={{ fontSize: "var(--text-xs)", color: "var(--status-connected)" }}>
                {t("watermark.stat.detected")}
              </div>
              <div
                style={{
                  fontSize: "var(--text-lg)",
                  fontWeight: "var(--weight-bold)",
                  color: "var(--status-connected)",
                }}
              >
                {detectedCount}
              </div>
            </div>

            <div
              style={{
                padding: "var(--space-2) var(--space-3)",
                borderRadius: "var(--radius-sm)",
                backgroundColor: "var(--bg-canvas)",
                border: "1px solid var(--border-subtle)",
              }}
            >
              <div style={{ fontSize: "var(--text-xs)", color: "var(--text-muted)" }}>
                {t("watermark.stat.clean")}
              </div>
              <div style={{ fontSize: "var(--text-lg)", fontWeight: "var(--weight-bold)" }}>
                {cleanCount}
              </div>
            </div>
          </div>

          {/* Single File Result Display */}
          {(!result.results || result.results.length <= 1) && result.image && (
            <div
              style={{
                padding: "var(--space-3)",
                borderRadius: "var(--radius-sm)",
                backgroundColor: "var(--bg-canvas)",
                border: "1px solid var(--border-subtle)",
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: "var(--space-2)",
              }}
            >
              <div style={{ overflow: "hidden", minWidth: 0 }}>
                <div style={{ fontSize: "var(--text-xs)", color: "var(--text-muted)" }}>
                  {t("watermark.result.path")}
                </div>
                <div
                  style={{
                    fontFamily: "var(--font-mono)",
                    fontSize: "var(--text-xs)",
                    color: "var(--text-primary)",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                >
                  {result.image.localUrl}
                </div>
              </div>

              <Button
                size="sm"
                onClick={() => handleCopy(result.image.localUrl)}
                title={t("watermark.copyPath")}
              >
                {copiedPath === result.image.localUrl ? <Check size={12} /> : <Copy size={12} />}
              </Button>
            </div>
          )}

          {/* Multiple Items Results List */}
          {result.results && result.results.length > 1 && (
            <div
              style={{
                maxHeight: 340,
                overflowY: "auto",
                display: "flex",
                flexDirection: "column",
                gap: "var(--space-1)",
              }}
            >
              {result.results.map((item, idx) => (
                <div
                  key={idx}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    padding: "var(--space-2) var(--space-3)",
                    borderRadius: "var(--radius-sm)",
                    backgroundColor: "var(--bg-canvas)",
                    border: "1px solid var(--border-subtle)",
                    fontSize: "var(--text-xs)",
                    gap: "var(--space-2)",
                  }}
                >
                  <div style={{ overflow: "hidden", minWidth: 0, flex: 1 }}>
                    <div
                      style={{
                        fontFamily: "var(--font-mono)",
                        color: "var(--text-primary)",
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {item.path}
                    </div>
                    {item.out_path && item.out_path !== item.path && (
                      <div
                        style={{
                          fontFamily: "var(--font-mono)",
                          color: "var(--text-muted)",
                          fontSize: "11px",
                          overflow: "hidden",
                          textOverflow: "ellipsis",
                          whiteSpace: "nowrap",
                        }}
                      >
                        → {item.out_path}
                      </div>
                    )}
                    {item.error && (
                      <div style={{ color: "var(--alert-error-text)" }}>{item.error}</div>
                    )}
                  </div>

                  <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
                    {item.error ? (
                      <Badge
                        style={{
                          backgroundColor: "var(--alert-error-bg)",
                          color: "var(--alert-error-text)",
                        }}
                      >
                        <AlertCircle size={11} style={{ marginRight: 3 }} />{" "}
                        {t("watermark.result.error")}
                      </Badge>
                    ) : item.detected ? (
                      <Badge
                        style={{
                          backgroundColor: "var(--status-connected-bg)",
                          color: "var(--status-connected)",
                        }}
                      >
                        <CheckCircle2 size={11} style={{ marginRight: 3 }} />{" "}
                        {t("watermark.result.detectedScore", {
                          score: Math.round(item.score * 100),
                        })}
                      </Badge>
                    ) : (
                      <Badge>
                        <Check size={11} style={{ marginRight: 3 }} />{" "}
                        {t("watermark.result.cleanBadge")}
                      </Badge>
                    )}

                    {item.out_path && (
                      <button
                        type="button"
                        onClick={() => handleCopy(item.out_path)}
                        style={{
                          border: "none",
                          background: "none",
                          cursor: "pointer",
                          padding: 2,
                          color: "var(--text-muted)",
                        }}
                        title={t("watermark.copyPath")}
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
