import React from "react";
import { useShallow } from "zustand/react/shallow";
import { Volume2 } from "lucide-react";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { MediaGenLayout } from "./MediaGenLayout";
import { MediaModelSelect } from "./MediaModelSelect";
import { OutputDirField } from "./OutputDirField";
import { useTTSGenStore } from "./useMediaGenStores";

export const TTSGenView: React.FC = () => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.TTS_GENERATE);
  const {
    text,
    model,
    voice,
    format,
    speed,
    language,
    outputDir,
    loading,
    progress,
    error,
    result,
    downloadingPath,
    setText,
    setModel,
    setVoice,
    setFormat,
    setSpeed,
    setLanguage,
    setOutputDir,
    generate,
    downloadResult,
    clearError,
    reset,
  } = useTTSGenStore(
    useShallow((s) => ({
      text: s.text,
      model: s.model,
      voice: s.voice,
      format: s.format,
      speed: s.speed,
      language: s.language,
      outputDir: s.outputDir,
      loading: s.loading,
      progress: s.progress,
      error: s.error,
      result: s.result,
      downloadingPath: s.downloadingPath,
      setText: s.setText,
      setModel: s.setModel,
      setVoice: s.setVoice,
      setFormat: s.setFormat,
      setSpeed: s.setSpeed,
      setLanguage: s.setLanguage,
      setOutputDir: s.setOutputDir,
      generate: s.generate,
      downloadResult: s.downloadResult,
      clearError: s.clearError,
      reset: s.reset,
    }))
  );

  const fmtParam = findParam(spec, "format");
  const voiceParam = findParam(spec, "voice");
  const speedParam = findParam(spec, "speed");
  const formatOptions = fmtParam?.options ?? [];
  const voiceOptions = voiceParam?.options ?? [];
  const activeFormat = format ?? fmtParam?.default ?? formatOptions[0] ?? "";

  useAgentScreenContext({
    feature: NodeTypes.TTS_GENERATE,
    selected_files: [],
    settings: {
      text,
      model,
      voice,
      ...(activeFormat ? { format: activeFormat } : {}),
      speed,
      ...(language.trim() ? { language: language.trim() } : {}),
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

  const canRun = text.trim().length > 0 && !loading;

  return (
    <MediaGenLayout
      kind="tts"
      title={t("mediagen.tts.title")}
      subtitle={t("mediagen.tts.subtitle")}
      settingsIcon={Volume2}
      actionIcon={Volume2}
      actionLabel={t("mediagen.tts.action.generate")}
      loading={loading}
      canRun={canRun}
      hasResult={Boolean(result)}
      onGenerate={() => void generate(spec)}
      onReset={reset}
      progress={progress}
      error={error}
      prompt={text}
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
      <MediaModelSelect purpose="tts" value={model} onChange={setModel} disabled={loading} />

      <div className="form-group">
        <label className="field-label" htmlFor="mediagen-tts-text">
          {t("mediagen.tts.field.text")}
        </label>
        <textarea
          id="mediagen-tts-text"
          rows={5}
          className="input-control mediagen-textarea"
          placeholder={t("mediagen.tts.textPlaceholder")}
          value={text}
          onChange={(e) => setText(e.target.value)}
          disabled={loading}
        />
      </div>

      <div className="mediagen-two-col">
        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-tts-voice">
            {t("mediagen.tts.field.voice")}
          </label>
          <input
            id="mediagen-tts-voice"
            type="text"
            list={voiceOptions.length > 0 ? "mediagen-voice-presets" : undefined}
            className="input-control"
            placeholder={t("mediagen.tts.voicePlaceholder")}
            value={voice}
            onChange={(e) => setVoice(e.target.value)}
            disabled={loading}
          />
          {voiceOptions.length > 0 && (
            <datalist id="mediagen-voice-presets">
              {voiceOptions.map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
          )}
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-tts-format">
            {t("mediagen.tts.field.format")}
          </label>
          <select
            id="mediagen-tts-format"
            className="input-control"
            value={formatOptions.length > 0 ? activeFormat : ""}
            onChange={(e) => setFormat(e.target.value)}
            disabled={loading || formatOptions.length === 0}
          >
            {formatOptions.length === 0 ? (
              <option value="">{t("mediagen.specsLoading")}</option>
            ) : (
              formatOptions.map((opt) => (
                <option key={opt} value={opt}>
                  {opt.toUpperCase()}
                </option>
              ))
            )}
          </select>
        </div>
      </div>

      <div className="mediagen-two-col">
        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-tts-speed">
            {t("mediagen.tts.field.speed")}
          </label>
          <input
            id="mediagen-tts-speed"
            type="number"
            step="0.1"
            min={speedParam?.min ?? 0.25}
            max={speedParam?.max ?? 4}
            className="input-control input-control--mono"
            value={speed}
            onChange={(e) => setSpeed(e.target.value)}
            disabled={loading}
          />
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="mediagen-tts-language">
            {t("mediagen.tts.field.language")}
          </label>
          <input
            id="mediagen-tts-language"
            type="text"
            className="input-control"
            placeholder={t("mediagen.tts.languagePlaceholder")}
            value={language}
            onChange={(e) => setLanguage(e.target.value)}
            disabled={loading}
          />
        </div>
      </div>

      <OutputDirField
        id="mediagen-tts-output-dir"
        value={outputDir}
        onChange={setOutputDir}
        disabled={loading}
      />
    </MediaGenLayout>
  );
};

export default TTSGenView;
