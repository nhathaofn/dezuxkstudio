import React from "react";
import { Loader2, RotateCcw, Volume2 } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { NodeTypes } from "../../contracts/generated";
import { useT } from "../../lib/i18n";
import { useAgentScreenContext } from "../agent/screenContext";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { MediaModelSelect } from "./MediaModelSelect";
import { MediaResultCard } from "./MediaResultCard";
import { OutputDirField } from "./OutputDirField";
import { useTTSGenStore } from "./useMediaGenStores";

const FALLBACK_FORMATS = ["wav", "mp3", "opus", "aac", "flac", "pcm"];
const VOICE_SUGGESTIONS = [
  "alloy",
  "nova",
  "echo",
  "Kore",
  "Puck",
  "Aoede",
  "Cherry",
  "Serena",
  "male-qn-qingse",
  "female-shaonv",
];

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
    reset,
  } = useTTSGenStore();

  const fmtParam = findParam(spec, "format");
  const formatOptions = fmtParam?.options ?? FALLBACK_FORMATS;
  const activeFormat = format ?? fmtParam?.default ?? "wav";

  useAgentScreenContext({
    feature: NodeTypes.TTS_GENERATE,
    selected_files: [],
    settings: {
      text,
      model,
      voice,
      format: activeFormat,
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
    <div className="dashboard-layout watermark-layout">
      <div className="dashboard-hero watermark-hero">
        <div className="watermark-hero-title-group">
          <h1 className="dashboard-hero-title watermark-hero-title">{t("mediagen.tts.title")}</h1>
          <div className="dashboard-hero-meta">
            <span>{t("mediagen.tts.subtitle")}</span>
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
            {loading ? <Loader2 size={13} className="animate-spin" /> : <Volume2 size={13} />}
            {loading ? t("mediagen.action.generating") : t("mediagen.tts.action.generate")}
          </Button>
        </div>
      </div>

      <div className="watermark-grid">
        <Card icon={Volume2} title={t("mediagen.card.settings")} className="card--flex-fill">
          <div className="watermark-config-stack">
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
                  list="mediagen-voice-presets"
                  className="input-control"
                  placeholder={t("mediagen.tts.voicePlaceholder")}
                  value={voice}
                  onChange={(e) => setVoice(e.target.value)}
                  disabled={loading}
                />
                <datalist id="mediagen-voice-presets">
                  {VOICE_SUGGESTIONS.map((v) => (
                    <option key={v} value={v} />
                  ))}
                </datalist>
              </div>

              <div className="form-group">
                <label className="field-label" htmlFor="mediagen-tts-format">
                  {t("mediagen.tts.field.format")}
                </label>
                <select
                  id="mediagen-tts-format"
                  className="input-control"
                  value={activeFormat}
                  onChange={(e) => setFormat(e.target.value)}
                  disabled={loading}
                >
                  {formatOptions.map((opt) => (
                    <option key={opt} value={opt}>
                      {opt.toUpperCase()}
                    </option>
                  ))}
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
                  min={0.25}
                  max={4}
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
          </div>
        </Card>

        <MediaResultCard
          kind="tts"
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

export default TTSGenView;
