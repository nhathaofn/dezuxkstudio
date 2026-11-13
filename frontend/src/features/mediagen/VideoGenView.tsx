import React from "react";
import { Film, Loader2, RotateCcw, Video as VideoIcon } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Switch } from "../../components/ui/Switch";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { useProvidersStore } from "../providers/useProvidersStore";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { MediaImagePicker } from "./MediaImagePicker";
import { MediaModelSelect } from "./MediaModelSelect";
import { MediaResultCard } from "./MediaResultCard";
import { OutputDirField } from "./OutputDirField";
import { resolveSelectedCandidate } from "./types";
import { useVideoGenStore } from "./useMediaGenStores";

const FALLBACK_ASPECTS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const FALLBACK_RESOLUTIONS = ["480p", "720p", "1080p"];
const FALLBACK_DURATIONS = [4, 5, 6, 8, 10, 12];

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
    reset,
  } = useVideoGenStore();

  const selectedCandidate = resolveSelectedCandidate(defaults, "video", model);
  const factResolutions = selectedCandidate?.model.facts.resolutions ?? [];
  const factDurations = selectedCandidate?.model.facts.durations ?? [];

  const aspectParam = findParam(spec, "aspect_ratio");
  const resParam = findParam(spec, "resolution");
  const aspectOptions = aspectParam?.options ?? FALLBACK_ASPECTS;
  const resolutionOptions =
    factResolutions.length > 0 ? factResolutions : (resParam?.options ?? FALLBACK_RESOLUTIONS);
  const durationOptions = factDurations.length > 0 ? factDurations : FALLBACK_DURATIONS;

  const activeAspect = aspectRatio ?? aspectParam?.default ?? "16:9";
  const activeResolution =
    resolution && resolutionOptions.includes(resolution)
      ? resolution
      : resolutionOptions.includes(resParam?.default ?? "720p")
        ? (resParam?.default ?? "720p")
        : (resolutionOptions[0] ?? "720p");
  const activeDuration = duration ?? durationOptions[0] ?? 5;

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
      aspect_ratio: activeAspect,
      resolution: activeResolution,
      duration: String(activeDuration),
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

  const canRun = (prompt.trim().length > 0 || startImage !== null || refs.length > 0) && !loading;

  return (
    <div className="dashboard-layout watermark-layout">
      <div className="dashboard-hero watermark-hero">
        <div className="watermark-hero-title-group">
          <h1 className="dashboard-hero-title watermark-hero-title">{t("mediagen.video.title")}</h1>
          <div className="dashboard-hero-meta">
            <span>{t("mediagen.video.subtitle")}</span>
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
            {loading ? <Loader2 size={13} className="animate-spin" /> : <Film size={13} />}
            {loading ? t("mediagen.action.generating") : t("mediagen.video.action.generate")}
          </Button>
        </div>
      </div>

      <div className="watermark-grid">
        <Card icon={VideoIcon} title={t("mediagen.card.settings")} className="card--flex-fill">
          <div className="watermark-config-stack">
            <MediaModelSelect
              purpose="video"
              value={model}
              onChange={setModel}
              disabled={loading}
            />

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
                <label className="field-label" htmlFor="mediagen-video-resolution">
                  {t("mediagen.field.resolution")}
                </label>
                <select
                  id="mediagen-video-resolution"
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

              <div className="form-group">
                <label className="field-label" htmlFor="mediagen-video-duration">
                  {t("mediagen.video.duration")}
                </label>
                <select
                  id="mediagen-video-duration"
                  className="input-control"
                  value={String(activeDuration)}
                  onChange={(e) => setDuration(Number.parseInt(e.target.value, 10))}
                  disabled={loading}
                >
                  {durationOptions.map((d) => (
                    <option key={d} value={String(d)}>
                      {d}s
                    </option>
                  ))}
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
                min={0}
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
          </div>
        </Card>

        <MediaResultCard
          kind="video"
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
                  duration: result.duration,
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

export default VideoGenView;
