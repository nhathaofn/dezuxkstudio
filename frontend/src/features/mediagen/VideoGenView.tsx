import React from "react";
import { useShallow } from "zustand/react/shallow";
import { Film, Video as VideoIcon } from "lucide-react";
import { Switch } from "../../components/ui/Switch";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { useProvidersStore } from "../providers/useProvidersStore";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { AspectRatioPresets, MediaGenLayout } from "./MediaGenLayout";
import { MediaImagePicker } from "./MediaImagePicker";
import { MediaModelSelect } from "./MediaModelSelect";
import { OutputDirField } from "./OutputDirField";
import { resolveSelectedCandidate } from "./types";
import { useVideoGenStore } from "./useMediaGenStores";

const STANDARD_VIDEO_DURATIONS = [4, 5, 6, 8, 10, 12];

export const VideoGenView: React.FC = () => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.VIDEO_GENERATE);
  const defaults = useProvidersStore((s) => s.defaults);
  const {
    prompt,
    startImage,
    endImage,
    refs,
    model,
    aspectRatio,
    resolution,
    duration,
    generateAudio,
    seed,
    outputDir,
    loading,
    progress,
    error,
    result,
    downloadingPath,
    setPrompt,
    setStartImage,
    setEndImage,
    addRefs,
    removeRef,
    setModel,
    setAspectRatio,
    setResolution,
    setDuration,
    setGenerateAudio,
    setSeed,
    setOutputDir,
    generate,
    downloadResult,
    clearError,
    reset,
  } = useVideoGenStore(
    useShallow((s) => ({
      prompt: s.prompt,
      startImage: s.startImage,
      endImage: s.endImage,
      refs: s.refs,
      model: s.model,
      aspectRatio: s.aspectRatio,
      resolution: s.resolution,
      duration: s.duration,
      generateAudio: s.generateAudio,
      seed: s.seed,
      outputDir: s.outputDir,
      loading: s.loading,
      progress: s.progress,
      error: s.error,
      result: s.result,
      downloadingPath: s.downloadingPath,
      setPrompt: s.setPrompt,
      setStartImage: s.setStartImage,
      setEndImage: s.setEndImage,
      addRefs: s.addRefs,
      removeRef: s.removeRef,
      setModel: s.setModel,
      setAspectRatio: s.setAspectRatio,
      setResolution: s.setResolution,
      setDuration: s.setDuration,
      setGenerateAudio: s.setGenerateAudio,
      setSeed: s.setSeed,
      setOutputDir: s.setOutputDir,
      generate: s.generate,
      downloadResult: s.downloadResult,
      clearError: s.clearError,
      reset: s.reset,
    }))
  );

  const selectedCandidate = resolveSelectedCandidate(defaults, "video", model);
  const factResolutions = selectedCandidate?.model.facts.resolutions ?? [];
  const factDurations = selectedCandidate?.model.facts.durations ?? [];

  const aspectParam = findParam(spec, "aspect_ratio");
  const resParam = findParam(spec, "resolution");
  const durParam = findParam(spec, "duration");
  const seedParam = findParam(spec, "seed");

  const aspectOptions = aspectParam?.options ?? [];
  const resolutionOptions =
    factResolutions.length > 0 ? factResolutions : (resParam?.options ?? []);
  const specDurationOptions =
    durParam?.options
      ?.map((o) => Number.parseInt(o, 10))
      .filter((n) => Number.isFinite(n) && n > 0) ??
    (durParam && (durParam.max ?? 0) > 0
      ? STANDARD_VIDEO_DURATIONS.filter(
          (d) => d >= Math.max(1, durParam.min ?? 1) && d <= (durParam.max ?? 0)
        )
      : []);
  const durationOptions = factDurations.length > 0 ? factDurations : specDurationOptions;

  const activeAspect = aspectRatio ?? aspectParam?.default ?? aspectOptions[0] ?? "";
  const activeResolution =
    resolution && resolutionOptions.includes(resolution)
      ? resolution
      : resParam?.default && resolutionOptions.includes(resParam.default)
        ? resParam.default
        : (resolutionOptions[0] ?? "");
  const activeDuration =
    duration && durationOptions.includes(duration) ? duration : (durationOptions[0] ?? 0);

  const selectedFiles = [
    ...(startImage ? [startImage.localPath || startImage.label] : []),
    ...(endImage ? [endImage.localPath || endImage.label] : []),
    ...refs.map((r) => r.localPath || r.label),
  ];

  useAgentScreenContext({
    feature: NodeTypes.VIDEO_GENERATE,
    selected_files: selectedFiles,
    settings: {
      prompt,
      model,
      ...(activeAspect ? { aspect_ratio: activeAspect } : {}),
      ...(activeResolution ? { resolution: activeResolution } : {}),
      ...(activeDuration > 0 ? { duration: String(activeDuration) } : {}),
      generate_audio: String(generateAudio),
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

  const canRun =
    (prompt.trim().length > 0 || Boolean(startImage) || Boolean(endImage) || refs.length > 0) &&
    !loading;

  return (
    <MediaGenLayout
      kind="video"
      title={t("mediagen.video.title")}
      subtitle={t("mediagen.video.subtitle")}
      settingsIcon={VideoIcon}
      actionIcon={Film}
      actionLabel={t("mediagen.video.action.generate")}
      loading={loading}
      canRun={canRun}
      hasResult={Boolean(result)}
      onGenerate={() => void generate(spec)}
      onReset={reset}
      progress={progress}
      error={error}
      prompt={prompt}
      sourcePath={startImage?.localPath || refs[0]?.localPath}
      result={
        result
          ? {
              path: result.path,
              mimeType: result.mimeType,
              provider: result.provider,
              model: result.model,
              sizeBytes: result.sizeBytes,
              duration: result.duration,
              asset_id: result.asset_id,
            }
          : null
      }
      downloadingPath={downloadingPath}
      onDownload={() => void downloadResult()}
      onRetry={canRun ? () => void generate(spec) : undefined}
      onDismissError={clearError}
    >
      <MediaModelSelect purpose="video" value={model} onChange={setModel} disabled={loading} />

      <div className="form-group">
        <label className="field-label" htmlFor="mediagen-video-prompt">
          {t("mediagen.field.prompt")}
        </label>
        <textarea
          id="mediagen-video-prompt"
          rows={4}
          className="input-control mediagen-textarea"
          placeholder={t("mediagen.video.promptPlaceholder")}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          disabled={loading}
        />
      </div>

      <div className="mediagen-two-col">
        <MediaImagePicker
          label={t("mediagen.video.startFrame")}
          items={startImage ? [startImage] : []}
          onAdd={(picked) => setStartImage(picked[0] ?? null)}
          onRemove={() => setStartImage(null)}
          disabled={loading}
        />
        <MediaImagePicker
          label={t("mediagen.video.endFrame")}
          items={endImage ? [endImage] : []}
          onAdd={(picked) => setEndImage(picked[0] ?? null)}
          onRemove={() => setEndImage(null)}
          disabled={loading}
        />
      </div>

      <MediaImagePicker
        label={t("mediagen.video.refsLabel")}
        multiple
        items={refs}
        onAdd={addRefs}
        onRemove={removeRef}
        disabled={loading}
      />

      <div className="mediagen-three-col">
        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-video-aspect">
            {t("mediagen.field.aspectRatio")}
          </label>
          <select
            id="mediagen-video-aspect"
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
          <label className="field-label" htmlFor="mediagen-video-resolution">
            {t("mediagen.field.resolution")}
          </label>
          <select
            id="mediagen-video-resolution"
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

        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-video-duration">
            {t("mediagen.video.duration")}
          </label>
          <select
            id="mediagen-video-duration"
            className="input-control"
            value={durationOptions.length > 0 && activeDuration > 0 ? String(activeDuration) : ""}
            onChange={(e) => setDuration(Number.parseInt(e.target.value, 10))}
            disabled={loading || durationOptions.length === 0}
          >
            {durationOptions.length === 0 ? (
              <option value="">{t("mediagen.specsLoading")}</option>
            ) : (
              durationOptions.map((d) => (
                <option key={d} value={String(d)}>
                  {d}s
                </option>
              ))
            )}
          </select>
        </div>
      </div>

      <div className="mediagen-switch-row">
        <div>
          <div className="field-label">{t("mediagen.video.generateAudio")}</div>
          <div className="muted-text">{t("mediagen.video.generateAudioHint")}</div>
        </div>
        <Switch
          checked={generateAudio}
          onCheckedChange={setGenerateAudio}
          disabled={loading}
          aria-label={t("mediagen.video.generateAudio")}
        />
      </div>

      <div className="form-group">
        <label className="field-label" htmlFor="mediagen-video-seed">
          {t("mediagen.field.seed")}
        </label>
        <input
          id="mediagen-video-seed"
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
        id="mediagen-video-output-dir"
        value={outputDir}
        onChange={setOutputDir}
        disabled={loading}
      />
    </MediaGenLayout>
  );
};

export default VideoGenView;
