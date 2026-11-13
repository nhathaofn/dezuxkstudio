import React from "react";
import { Image as ImageIcon, Loader2, RotateCcw, Sparkles } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { MediaImagePicker } from "./MediaImagePicker";
import { MediaModelSelect } from "./MediaModelSelect";
import { MediaResultCard } from "./MediaResultCard";
import { OutputDirField } from "./OutputDirField";
import { useImageGenStore } from "./useMediaGenStores";

const FALLBACK_ASPECTS = ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"];
const FALLBACK_RESOLUTIONS = ["1K", "2K", "4K"];

export const ImageGenView: React.FC = () => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.IMAGE_GENERATE);
  const {
    prompt,
    refs,
    model,
    aspectRatio,
    resolution,
    seed,
    outputDir,
    loading,
    progress,
    error,
    result,
    downloadingPath,
    setPrompt,
    addRefs,
    removeRef,
    setModel,
    setAspectRatio,
    setResolution,
    setSeed,
    setOutputDir,
    generate,
    downloadResult,
    reset,
  } = useImageGenStore();

  const aspectParam = findParam(spec, "aspect_ratio");
  const resParam = findParam(spec, "resolution");
  const aspectOptions = aspectParam?.options ?? FALLBACK_ASPECTS;
  const resolutionOptions = resParam?.options ?? FALLBACK_RESOLUTIONS;
  const activeAspect = aspectRatio ?? aspectParam?.default ?? "1:1";
  const activeResolution = resolution ?? resParam?.default ?? "1K";

  useAgentScreenContext({
    feature: NodeTypes.IMAGE_GENERATE,
    selected_files: refs.map((r) => r.localPath || r.label),
    settings: {
      prompt,
      model,
      aspect_ratio: activeAspect,
      resolution: activeResolution,
      ...(seed.trim() ? { seed: seed.trim() } : {}),
      ...(outputDir.trim() ? { output_dir: outputDir.trim() } : {}),
    },
    last_result: loading
      ? "generating..."
      : error
        ? `error: ${error.message}`
        : result
          ? `${result.path} (${result.provider}/${result.model})`
          : "",
  });

  const canRun = prompt.trim().length > 0 && !loading;

  return (
    <div className="dashboard-layout watermark-layout">
      <div className="dashboard-hero watermark-hero">
        <div className="watermark-hero-title-group">
          <h1 className="dashboard-hero-title watermark-hero-title">{t("mediagen.image.title")}</h1>
          <div className="dashboard-hero-meta">
            <span>{t("mediagen.image.subtitle")}</span>
            {refs.length > 0 && <Badge>{t("mediagen.badge.refs", { count: refs.length })}</Badge>}
          </div>
        </div>

        <div className="watermark-hero-actions">
          <Button variant="ghost" size="sm" onClick={reset} disabled={loading && !result}>
            <RotateCcw size={13} />
            {t("watermark.resetBtn")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => void generate(spec)}
            disabled={!canRun}
          >
            {loading ? <Loader2 size={13} className="animate-spin" /> : <Sparkles size={13} />}
            {loading ? t("mediagen.action.generating") : t("mediagen.image.action.generate")}
          </Button>
        </div>
      </div>

      <div className="watermark-grid">
        <Card icon={ImageIcon} title={t("mediagen.card.settings")} className="card--flex-fill">
          <div className="watermark-config-stack">
            <MediaModelSelect
              purpose="image"
              value={model}
              onChange={setModel}
              disabled={loading}
            />

            <div className="form-group">
              <label className="field-label" htmlFor="mediagen-image-prompt">
                {t("mediagen.field.prompt")}
              </label>
              <textarea
                id="mediagen-image-prompt"
                rows={4}
                className="input-control mediagen-textarea"
                placeholder={t("mediagen.image.promptPlaceholder")}
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                disabled={loading}
              />
            </div>

            <MediaImagePicker
              label={t("mediagen.image.refsLabel")}
              multiple
              items={refs}
              onAdd={addRefs}
              onRemove={removeRef}
              disabled={loading}
            />

            <div className="mediagen-two-col">
              <div className="form-group">
                <label className="field-label" htmlFor="mediagen-image-aspect">
                  {t("mediagen.field.aspectRatio")}
                </label>
                <select
                  id="mediagen-image-aspect"
                  className="input-control"
                  value={activeAspect}
                  onChange={(e) => setAspectRatio(e.target.value)}
                  disabled={loading}
                >
                  {aspectOptions.map((opt) => (
                    <option key={opt} value={opt}>
                      {opt}
                    </option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label className="field-label" htmlFor="mediagen-image-resolution">
                  {t("mediagen.field.resolution")}
                </label>
                <select
                  id="mediagen-image-resolution"
                  className="input-control"
                  value={activeResolution}
                  onChange={(e) => setResolution(e.target.value)}
                  disabled={loading}
                >
                  {resolutionOptions.map((opt) => (
                    <option key={opt} value={opt}>
                      {opt}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="form-group">
              <label className="field-label" htmlFor="mediagen-image-seed">
                {t("mediagen.field.seed")}
              </label>
              <input
                id="mediagen-image-seed"
                type="number"
                min={0}
                className="input-control input-control--mono"
                placeholder={t("mediagen.field.seedPlaceholder")}
                value={seed}
                onChange={(e) => setSeed(e.target.value)}
                disabled={loading}
              />
            </div>

            <OutputDirField
              id="mediagen-image-output-dir"
              value={outputDir}
              onChange={setOutputDir}
              disabled={loading}
            />
          </div>
        </Card>

        <MediaResultCard
          kind="image"
          loading={loading}
          progress={progress}
          error={error}
          result={
            result
              ? {
                  path: result.path,
                  mimeType: result.mimeType,
                  provider: result.provider,
                  model: result.model,
                  sizeBytes: result.sizeBytes,
                }
              : null
          }
          downloadingPath={downloadingPath}
          onDownload={() => void downloadResult()}
        />
      </div>
    </div>
  );
};

export default ImageGenView;
