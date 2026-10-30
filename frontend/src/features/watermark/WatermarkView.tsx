import React, { useRef, useState } from "react";
import { Sparkles, UploadCloud, CheckCircle2, Check } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Card";
import { ErrorText } from "../../components/ui/ErrorText";
import { getBridge } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { useWatermarkStore } from "./useWatermarkStore";

export const WatermarkView: React.FC = () => {
  const t = useT();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [dragActive, setDragActive] = useState(false);

  const {
    inputPath,
    inPlace,
    outputDir,
    loading,
    result,
    error,
    setInputPath,
    setInPlace,
    setOutputDir,
    removeWatermark,
    reset,
  } = useWatermarkStore();

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (bridge?.BrowseImage) {
      try {
        const selected = await bridge.BrowseImage(t("watermark.browse.title"));
        if (selected) {
          setInputPath(selected);
        }
      } catch (err) {
        console.error("BrowseImage failed", err);
      }
    } else {
      fileInputRef.current?.click();
    }
  };

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) {
      const filePath = (file as unknown as { path?: string }).path || file.name;
      setInputPath(filePath);
    }
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(true);
  };

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragActive(false);
    const file = e.dataTransfer.files?.[0];
    if (file) {
      const filePath = (file as unknown as { path?: string }).path || file.name;
      setInputPath(filePath);
    }
  };

  return (
    <div className="dashboard-layout">
      {/* Header */}
      <div className="dashboard-hero">
        <div>
          <h1 className="dashboard-hero-title">{t("watermark.title")}</h1>
          <div className="dashboard-hero-meta">
            <span>{t("watermark.subtitle")}</span>
          </div>
        </div>
      </div>

      {/* Main card */}
      <Card
        icon={Sparkles}
        title={t("watermark.card.title")}
        subtitle={t("watermark.card.subtitle")}
        wide
      >
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-4)" }}>
          {/* Dropzone */}
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            onClick={handleBrowse}
            style={{
              border: `2px dashed ${dragActive ? "var(--color-primary)" : "var(--color-border)"}`,
              borderRadius: "var(--radius-lg)",
              padding: "var(--space-6)",
              textAlign: "center",
              cursor: "pointer",
              backgroundColor: dragActive
                ? "var(--color-primary-subtle)"
                : "var(--color-surface-subtle)",
              transition: "all 0.2s ease",
            }}
          >
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              style={{ display: "none" }}
              onChange={handleFileInputChange}
            />
            <UploadCloud
              size={36}
              style={{ color: "var(--color-primary)", margin: "0 auto var(--space-2)" }}
            />
            <h3 style={{ margin: "0 0 var(--space-1)", fontSize: "var(--font-size-md)" }}>
              {inputPath ? t("watermark.selectedFile") : t("watermark.dragOrClick")}
            </h3>
            <p
              style={{
                margin: 0,
                color: "var(--color-text-muted)",
                fontSize: "var(--font-size-sm)",
                wordBreak: "break-all",
              }}
            >
              {inputPath || t("watermark.supportedFormats")}
            </p>
          </div>

          {/* Options */}
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))",
              gap: "var(--space-3)",
              padding: "var(--space-3)",
              backgroundColor: "var(--color-surface-subtle)",
              borderRadius: "var(--radius-md)",
            }}
          >
            <label
              style={{
                display: "flex",
                alignItems: "center",
                gap: "var(--space-2)",
                cursor: "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={inPlace}
                onChange={(e) => setInPlace(e.target.checked)}
                style={{ width: 16, height: 16 }}
              />
              <div>
                <div style={{ fontWeight: 500, fontSize: "var(--font-size-sm)" }}>
                  {t("watermark.inplace")}
                </div>
                <div style={{ color: "var(--color-text-muted)", fontSize: "var(--font-size-xs)" }}>
                  {t("watermark.inplace.desc")}
                </div>
              </div>
            </label>

            {!inPlace && (
              <div>
                <label
                  style={{
                    display: "block",
                    fontSize: "var(--font-size-sm)",
                    fontWeight: 500,
                    marginBottom: "var(--space-1)",
                  }}
                >
                  {t("watermark.outputDir")}
                </label>
                <input
                  type="text"
                  placeholder={t("watermark.outputDir.placeholder")}
                  value={outputDir}
                  onChange={(e) => setOutputDir(e.target.value)}
                  style={{
                    width: "100%",
                    padding: "var(--space-2)",
                    borderRadius: "var(--radius-sm)",
                    border: "1px solid var(--color-border)",
                    backgroundColor: "var(--color-surface)",
                    color: "var(--color-text)",
                  }}
                />
              </div>
            )}
          </div>

          {/* Actions */}
          <div style={{ display: "flex", gap: "var(--space-2)" }}>
            <Button
              variant="primary"
              disabled={!inputPath || loading}
              onClick={() => void removeWatermark()}
              style={{ display: "inline-flex", alignItems: "center", gap: "var(--space-2)" }}
            >
              <Sparkles size={15} />
              {loading ? t("watermark.processing") : t("watermark.startBtn")}
            </Button>

            {(inputPath || result) && (
              <Button onClick={reset} disabled={loading}>
                {t("watermark.resetBtn")}
              </Button>
            )}
          </div>

          <ErrorText error={error} />

          {/* Result card */}
          {result && (
            <div
              style={{
                marginTop: "var(--space-3)",
                padding: "var(--space-4)",
                borderRadius: "var(--radius-md)",
                border: "1px solid var(--color-border)",
                backgroundColor: "var(--color-surface)",
              }}
            >
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: "var(--space-2)",
                  marginBottom: "var(--space-2)",
                }}
              >
                {result.detected ? (
                  <Badge
                    style={{
                      backgroundColor: "var(--color-success-subtle)",
                      color: "var(--color-success)",
                    }}
                  >
                    <CheckCircle2 size={13} style={{ marginRight: 4 }} />
                    {t("watermark.result.detected")}
                  </Badge>
                ) : (
                  <Badge>
                    <Check size={13} style={{ marginRight: 4 }} />
                    {t("watermark.result.clean")}
                  </Badge>
                )}
              </div>

              <div style={{ fontSize: "var(--font-size-sm)", wordBreak: "break-all" }}>
                <strong>{t("watermark.result.path")}: </strong>
                <code>{result.image.localUrl}</code>
              </div>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
};

export default WatermarkView;
