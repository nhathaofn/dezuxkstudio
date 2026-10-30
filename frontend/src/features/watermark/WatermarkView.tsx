import React, { useRef, useState } from "react";
import {
  Sparkles,
  UploadCloud,
  CheckCircle2,
  Check,
  AlertCircle,
  Cpu,
  Crosshair,
  FileImage,
  Loader2,
  RotateCcw,
  Copy,
  Layers,
  FolderOpen,
} from "lucide-react";
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
  const [copiedPath, setCopiedPath] = useState<string | null>(null);

  const {
    inputPath,
    inputPaths,
    inPlace,
    outputDir,
    workers,
    corner,
    loading,
    result,
    error,
    setInputPath,
    setInputPaths,
    setInPlace,
    setOutputDir,
    setWorkers,
    setCorner,
    removeWatermark,
    reset,
  } = useWatermarkStore();

  const handleBrowse = async () => {
    const bridge = getBridge();
    if (bridge?.BrowseImages) {
      try {
        const selected = await bridge.BrowseImages(t("watermark.browse.title"));
        if (selected && selected.length > 0) {
          setInputPaths(selected);
        }
      } catch (err) {
        console.error("BrowseImages failed", err);
      }
    } else if (bridge?.BrowseImage) {
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

  const handleBrowseOutputDir = async () => {
    const bridge = getBridge();
    if (bridge?.BrowseDirectory) {
      try {
        const selected = await bridge.BrowseDirectory(t("watermark.outputDir"));
        if (selected) {
          setOutputDir(selected);
        }
      } catch (err) {
        console.error("BrowseDirectory failed", err);
      }
    }
  };

  const extractPathsFromFileList = (files: FileList | null): string[] => {
    if (!files || files.length === 0) return [];
    const paths: string[] = [];
    for (let i = 0; i < files.length; i++) {
      const file = files[i];
      const filePath = (file as unknown as { path?: string }).path || file.name;
      if (filePath) paths.push(filePath);
    }
    return paths;
  };

  const handleFileInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const paths = extractPathsFromFileList(e.target.files);
    if (paths.length > 0) {
      setInputPaths(paths);
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
    const paths = extractPathsFromFileList(e.dataTransfer.files);
    if (paths.length > 0) {
      setInputPaths(paths);
    }
  };

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
    <div className="dashboard-layout" style={{ maxWidth: 1280 }}>
      {/* Header Bar */}
      <div className="dashboard-hero" style={{ padding: "var(--space-3) var(--space-5)" }}>
        <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
          <h1 className="dashboard-hero-title" style={{ margin: 0, fontSize: "var(--text-xl)" }}>
            {t("watermark.title")}
          </h1>
          <div className="dashboard-hero-meta" style={{ gap: "var(--space-2)" }}>
            <Badge>Google Flow / Imagen</Badge>
            <Badge>Multi-threaded</Badge>
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
        {/* Left Column: Input Source & Config */}
        <Card icon={Sparkles} title={t("watermark.card.input")}>
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
            {/* Compact Dropzone */}
            <div
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
              onClick={handleBrowse}
              style={{
                border: `1px dashed ${dragActive ? "var(--border-focus)" : "var(--border-default)"}`,
                borderRadius: "var(--radius-md)",
                padding: "var(--space-3)",
                textAlign: "center",
                cursor: "pointer",
                backgroundColor: dragActive ? "var(--bg-surface-elevated)" : "var(--bg-canvas)",
                transition: "all var(--transition-fast)",
              }}
            >
              <input
                ref={fileInputRef}
                type="file"
                multiple
                accept="image/png,image/jpeg,image/webp"
                style={{ display: "none" }}
                onChange={handleFileInputChange}
              />
              <UploadCloud
                size={22}
                style={{
                  color: dragActive ? "var(--border-focus)" : "var(--text-muted)",
                  margin: "0 auto var(--space-1)",
                }}
              />
              <div
                style={{
                  fontSize: "var(--text-sm)",
                  fontWeight: "var(--weight-medium)",
                  color: "var(--text-primary)",
                  marginBottom: 2,
                }}
              >
                {totalFiles > 0
                  ? t("watermark.readyFiles", { count: totalFiles })
                  : t("watermark.dragOrClick")}
              </div>
              <div
                style={{
                  fontSize: "var(--text-xs)",
                  color: "var(--text-muted)",
                }}
              >
                {totalFiles > 0 ? t("watermark.changeFiles") : t("watermark.dragOrClickDesc")}
              </div>
            </div>

            {/* Config Fields */}
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
              {/* Corner Selection */}
              <div className="form-group">
                <label
                  className="field-label"
                  style={{ display: "flex", alignItems: "center", gap: 4 }}
                >
                  <Crosshair size={12} />
                  {t("watermark.corner")}
                </label>
                <select
                  value={corner}
                  onChange={(e) => setCorner(e.target.value)}
                  className="input-control"
                  style={{
                    height: 36,
                    padding: "4px var(--select-padding-right, 36px) 4px 10px",
                    fontSize: "var(--text-sm)",
                    lineHeight: "normal",
                  }}
                >
                  <option value="bottom-right">{t("watermark.corner.bottom_right")}</option>
                  <option value="bottom-left">{t("watermark.corner.bottom_left")}</option>
                  <option value="top-right">{t("watermark.corner.top_right")}</option>
                  <option value="top-left">{t("watermark.corner.top_left")}</option>
                  <option value="any">{t("watermark.corner.any")}</option>
                </select>
              </div>

              {/* Multi-threading Workers */}
              <div className="form-group">
                <label
                  className="field-label"
                  style={{ display: "flex", alignItems: "center", gap: 4 }}
                >
                  <Cpu size={12} />
                  {t("watermark.workers")}
                </label>
                <select
                  value={workers}
                  onChange={(e) => setWorkers(Number(e.target.value))}
                  className="input-control"
                  style={{
                    height: 36,
                    padding: "4px var(--select-padding-right, 36px) 4px 10px",
                    fontSize: "var(--text-sm)",
                    lineHeight: "normal",
                  }}
                >
                  <option value={0}>{t("watermark.workers.auto")}</option>
                  <option value={2}>2 Workers</option>
                  <option value={4}>4 Workers</option>
                  <option value={8}>8 Workers</option>
                  <option value={16}>16 Workers</option>
                </select>
              </div>

              {/* Output Mode Switch */}
              <div className="form-group">
                <label className="field-label">{t("watermark.outputMode")}</label>
                <div
                  style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "var(--space-2)" }}
                >
                  <Button
                    size="sm"
                    variant={!inPlace ? "primary" : "secondary"}
                    onClick={() => setInPlace(false)}
                    style={{ fontSize: "var(--text-xs)" }}
                  >
                    {t("watermark.mode.copy")}
                  </Button>
                  <Button
                    size="sm"
                    variant={inPlace ? "primary" : "secondary"}
                    onClick={() => setInPlace(true)}
                    style={{ fontSize: "var(--text-xs)" }}
                  >
                    {t("watermark.mode.inplace")}
                  </Button>
                </div>
              </div>

              {/* Output Directory Input with Folder Picker */}
              {!inPlace && (
                <div className="form-group">
                  <label className="field-label">{t("watermark.outputDir")}</label>
                  <div style={{ display: "flex", gap: "var(--space-2)" }}>
                    <input
                      type="text"
                      placeholder={t("watermark.outputDir.placeholder")}
                      value={outputDir}
                      onChange={(e) => setOutputDir(e.target.value)}
                      className="input-control"
                      style={{
                        flex: 1,
                        height: 36,
                        padding: "6px 10px",
                        fontSize: "var(--text-xs)",
                        fontFamily: "var(--font-mono)",
                        lineHeight: "normal",
                      }}
                    />
                    <Button
                      type="button"
                      size="sm"
                      onClick={handleBrowseOutputDir}
                      title={t("watermark.browseFolder")}
                      style={{ height: 36, padding: "0 var(--space-3)" }}
                    >
                      <FolderOpen size={14} />
                    </Button>
                  </div>
                </div>
              )}
            </div>
          </div>
        </Card>

        {/* Right Column: Queue & Live Results */}
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
              <Badge>{totalFiles} files</Badge>
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
              <div
                style={{ fontSize: "var(--text-xs)", color: "var(--text-secondary)", marginTop: 2 }}
              >
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
                    <Badge>Ready</Badge>
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
                    title="Copy path"
                  >
                    {copiedPath === result.image.localUrl ? (
                      <Check size={12} />
                    ) : (
                      <Copy size={12} />
                    )}
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
                            <AlertCircle size={11} style={{ marginRight: 3 }} /> Lỗi
                          </Badge>
                        ) : item.detected ? (
                          <Badge
                            style={{
                              backgroundColor: "var(--status-connected-bg)",
                              color: "var(--status-connected)",
                            }}
                          >
                            <CheckCircle2 size={11} style={{ marginRight: 3 }} /> Đã xóa (
                            {Math.round(item.score * 100)}%)
                          </Badge>
                        ) : (
                          <Badge>
                            <Check size={11} style={{ marginRight: 3 }} /> Sạch
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
                            title="Copy path"
                          >
                            {copiedPath === item.out_path ? (
                              <Check size={12} />
                            ) : (
                              <Copy size={12} />
                            )}
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
      </div>
    </div>
  );
};

export default WatermarkView;
