import React from "react";
import { useShallow } from "zustand/react/shallow";
import { Image as ImageIcon, Sparkles } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { AspectRatioPresets, MediaGenLayout } from "./MediaGenLayout";
import { MediaImagePicker } from "./MediaImagePicker";
import { MediaModelSelect } from "./MediaModelSelect";
import { OutputDirField } from "./OutputDirField";
import { useImageGenStore } from "./useMediaGenStores";

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
    clearError,
    reset,
  } = useImageGenStore(
    useShallow((s) => ({
      prompt: s.prompt,
      refs: s.refs,
      model: s.model,
      aspectRatio: s.aspectRatio,
      resolution: s.resolution,
      seed: s.seed,
      outputDir: s.outputDir,
      loading: s.loading,
      progress: s.progress,
      error: s.error,
      result: s.result,
      downloadingPath: s.downloadingPath,
      setPrompt: s.setPrompt,
      addRefs: s.addRefs,
      removeRef: s.removeRef,
      setModel: s.setModel,
      setAspectRatio: s.setAspectRatio,
      setResolution: s.setResolution,
      setSeed: s.setSeed,
      setOutputDir: s.setOutputDir,
      generate: s.generate,
      downloadResult: s.downloadResult,
      clearError: s.clearError,
      reset: s.reset,
    }))
  );

  const aspectParam = findParam(spec, "aspect_ratio");
  const resParam = findParam(spec, "resolution");
  const seedParam = findParam(spec, "seed");
  const aspectOptions = aspectParam?.options ?? [];
  const resolutionOptions = resParam?.options ?? [];
  const activeAspect = aspectRatio ?? aspectParam?.default ?? aspectOptions[0] ?? "";
  const activeResolution = resolution ?? resParam?.default ?? resolutionOptions[0] ?? "";

  useAgentScreenContext({
    feature: NodeTypes.IMAGE_GENERATE,
    selected_files: refs.map((r) => r.localPath || r.label),
    settings: {
      prompt,
      model,
      ...(activeAspect ? { aspect_ratio: activeAspect } : {}),
      ...(activeResolution ? { resolution: activeResolution } : {}),
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
    <MediaGenLayout
      kind="image"
      title={t("mediagen.image.title")}
      subtitle={t("mediagen.image.subtitle")}
      heroMeta={
        refs.length > 0 ? <Badge>{t("mediagen.badge.refs", { count: refs.length })}</Badge> : null
      }
      settingsIcon={ImageIcon}
      actionIcon={Sparkles}
      actionLabel={t("mediagen.image.action.generate")}
      loading={loading}
      canRun={canRun}
      hasResult={Boolean(result)}
      onGenerate={() => void generate(spec)}
      onReset={reset}
      progress={progress}
      error={error}
      prompt={prompt}
      sourcePath={refs[0]?.localPath}
      result={
        result
          ? {
              path: result.path,
              mimeType: result.mimeType,
              provider: result.provider,
              model: result.model,
              sizeBytes: result.sizeBytes,
              asset_id: result.asset_id,
            }
          : null
      }
      downloadingPath={downloadingPath}
      onDownload={() => void downloadResult()}
      onRetry={canRun ? () => void generate(spec) : undefined}
      onDismissError={clearError}
    >
      <MediaModelSelect purpose="image" value={model} onChange={setModel} disabled={loading} />

      <div className="form-group">
        <label className="field-label" htmlFor="mediagen-image-prompt">
          {t("mediagen.field.prompt")}
        </label>
        <textarea
          id="mediagen-image-prompt"
          rows={5}
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
            value={aspectOptions.length > 0 ? activeAspect : ""}
            onChange={(e) => setAspectRatio(e.target.value)}
            disabled={loading || aspectOptions.length === 0}
          >
            {aspectOptions.length === 0 ? (
              <option value="">{t("mediagen.specsLoading")}</option>
            ) : (
              aspectOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))
            )}
          </select>
          <AspectRatioPresets
            options={aspectOptions}
            active={activeAspect}
            disabled={loading}
            onSelect={setAspectRatio}
          />
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-image-resolution">
            {t("mediagen.field.resolution")}
          </label>
          <select
            id="mediagen-image-resolution"
            className="input-control"
            value={resolutionOptions.length > 0 ? activeResolution : ""}
            onChange={(e) => setResolution(e.target.value)}
            disabled={loading || resolutionOptions.length === 0}
          >
            {resolutionOptions.length === 0 ? (
              <option value="">{t("mediagen.specsLoading")}</option>
            ) : (
              resolutionOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt}
                </option>
              ))
            )}
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
          min={seedParam?.min ?? 0}
          max={seedParam?.max}
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
    </MediaGenLayout>
  );
};

export default ImageGenView;
