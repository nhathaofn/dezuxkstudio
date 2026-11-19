import React, { useState } from "react";
import {
  Bot,
  Film,
  Image as ImageIcon,
  Layers,
  Loader2,
  Play,
  Speech,
  Volume2,
} from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Switch } from "../../components/ui/Switch";
import { appConfig } from "../../config/appConfig";
import {
  NodeTypes,
  type ProbeMediaOutput,
  type StudioEpisodeDetailResult,
  type StudioShot,
} from "../../contracts/generated";
import { isMessageKey, useT } from "../../lib/i18n";
import { askAgentAboutMediaAsset } from "../agent/screenContext";
import { findParam, useNodeSpec } from "../workflow/useNodeSpecs";
import { SignedStudioAudio, SignedStudioVideo } from "./SignedStudioMedia";
import { targetVersionKey, useStudioStore } from "./useStudioStore";

const ShotPostToolsPanel: React.FC<{
  shot: StudioShot;
  ttsModel: string;
  busy: boolean;
}> = ({ shot, ttsModel, busy }) => {
  const t = useT();
  const dubSpec = useNodeSpec(NodeTypes.TTS_DUB_MUX);
  const lipSpec = useNodeSpec(NodeTypes.LIPSYNC_GENERATE);
  const overflowParam = findParam(dubSpec, "overflow_mode");
  const duckParam = findParam(dubSpec, "duck_original");
  const volParam = findParam(dubSpec, "original_volume");
  const faceParam = findParam(lipSpec, "face_index");

  const runShotDubMux = useStudioStore((s) => s.runShotDubMux);
  const runShotLipSync = useStudioStore((s) => s.runShotLipSync);

  const [mode, setMode] = useState<"none" | "dub" | "lipsync">("none");
  const [voice, setVoice] = useState("");
  const [overflowMode, setOverflowMode] = useState("");
  const [duckOriginal, setDuckOriginal] = useState(
    duckParam?.default ? duckParam.default !== "false" : true
  );
  const [origVolume, setOrigVolume] = useState(volParam?.default ?? "0.25");
  const [customAudio, setCustomAudio] = useState("");

  const [lipModelRef, setLipModelRef] = useState("");
  const [lipFaceIndex, setLipFaceIndex] = useState(Number(faceParam?.default ?? 0));
  const [lipVideoOverride, setLipVideoOverride] = useState("");
  const [lipAudioOverride, setLipAudioOverride] = useState("");
  const [lipPrompt, setLipPrompt] = useState("");

  const overflowOptions = overflowParam?.options ?? [];
  const effectiveOverflow =
    overflowMode || overflowParam?.default || overflowOptions[0] || "freeze_last_frame";

  return (
    <div className="studio-form-stack">
      <div className="studio-toolbar">
        <Button
          variant={mode === "dub" ? "primary" : "ghost"}
          size="sm"
          onClick={() => setMode((m) => (m === "dub" ? "none" : "dub"))}
          disabled={busy || !shot.video_path}
        >
          <Volume2 size={13} />
          {t("studio.video.dubMux")}
        </Button>
        <Button
          variant={mode === "lipsync" ? "primary" : "ghost"}
          size="sm"
          onClick={() => setMode((m) => (m === "lipsync" ? "none" : "lipsync"))}
          disabled={busy || (!shot.video_path && !lipVideoOverride.trim())}
        >
          <Speech size={13} />
          {t("studio.video.lipsync")}
        </Button>
      </div>

      {mode === "dub" && (
        <div className="studio-form-stack">
          <div className="studio-field-row">
            <div className="form-group">
              <label className="field-label" htmlFor={`dub-voice-${shot.id}`}>
                {t("mediagen.tts.field.voice")}
              </label>
              <input
                id={`dub-voice-${shot.id}`}
                className="input-control"
                placeholder={t("studio.video.voicePlaceholder")}
                value={voice}
                onChange={(e) => setVoice(e.target.value)}
                disabled={busy}
              />
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`dub-overflow-${shot.id}`}>
                {t("studio.video.overflowMode")}
              </label>
              <select
                id={`dub-overflow-${shot.id}`}
                className="input-control"
                value={effectiveOverflow}
                onChange={(e) => setOverflowMode(e.target.value)}
                disabled={busy || overflowOptions.length === 0}
              >
                {overflowOptions.map((opt) => {
                  const key = `studio.video.overflow.${opt}`;
                  return (
                    <option key={opt} value={opt}>
                      {isMessageKey(key) ? t(key) : opt}
                    </option>
                  );
                })}
              </select>
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`dub-vol-${shot.id}`}>
                {t("studio.video.originalVolume")}
              </label>
              <input
                id={`dub-vol-${shot.id}`}
                type="number"
                step="0.05"
                min={0}
                max={2}
                className="input-control"
                value={origVolume}
                onChange={(e) => setOrigVolume(e.target.value)}
                disabled={busy}
              />
            </div>
          </div>
          <div className="studio-field-row">
            <label className="studio-inline-toggle">
              <Switch
                checked={duckOriginal}
                onCheckedChange={setDuckOriginal}
                aria-label={t("studio.video.duckOriginal")}
                disabled={busy}
              />
              <span>{t("studio.video.duckOriginal")}</span>
            </label>
            <div className="form-group">
              <input
                className="input-control input-control--mono"
                placeholder={t("studio.video.audioPathOverride")}
                value={customAudio}
                onChange={(e) => setCustomAudio(e.target.value)}
                disabled={busy}
              />
            </div>
            <Button
              variant="primary"
              size="sm"
              disabled={busy || !shot.video_path}
              onClick={() =>
                void runShotDubMux({
                  shot,
                  audioPath: customAudio || undefined,
                  modelRef: ttsModel || undefined,
                  voice: voice || undefined,
                  overflowMode: effectiveOverflow,
                  duckOriginal,
                  originalVolume: Number.isFinite(Number(origVolume))
                    ? Number(origVolume)
                    : undefined,
                })
              }
            >
              {t("studio.video.runDubMux")}
            </Button>
          </div>
        </div>
      )}

      {mode === "lipsync" && (
        <div className="studio-form-stack">
          <div className="studio-field-row">
            <div className="form-group">
              <label className="field-label" htmlFor={`ls-model-${shot.id}`}>
                {t("studio.video.modelRef")}
              </label>
              <input
                id={`ls-model-${shot.id}`}
                className="input-control input-control--mono"
                value={lipModelRef}
                onChange={(e) => setLipModelRef(e.target.value)}
                disabled={busy}
              />
            </div>
            <div className="form-group">
              <label className="field-label" htmlFor={`ls-face-${shot.id}`}>
                {t("studio.video.faceIndex")}
              </label>
              <input
                id={`ls-face-${shot.id}`}
                type="number"
                min={faceParam?.min ?? 0}
                max={faceParam?.max}
                className="input-control"
                value={lipFaceIndex}
                onChange={(e) => setLipFaceIndex(Math.max(0, Number(e.target.value) || 0))}
                disabled={busy}
              />
            </div>
          </div>
          <div className="studio-field-row">
            <input
              className="input-control input-control--mono"
              placeholder={shot.video_path || t("studio.video.videoPathOverride")}
              value={lipVideoOverride}
              onChange={(e) => setLipVideoOverride(e.target.value)}
              disabled={busy}
            />
            <input
              className="input-control input-control--mono"
              placeholder={shot.audio_path || t("studio.video.audioPathOverride")}
              value={lipAudioOverride}
              onChange={(e) => setLipAudioOverride(e.target.value)}
              disabled={busy}
            />
          </div>
          <div className="studio-toolbar">
            <input
              className="input-control"
              placeholder={t("studio.video.lipsyncPrompt")}
              value={lipPrompt}
              onChange={(e) => setLipPrompt(e.target.value)}
              disabled={busy}
            />
            <Button
              variant="primary"
              size="sm"
              disabled={
                busy ||
                !(lipVideoOverride.trim() || shot.video_path) ||
                !(lipAudioOverride.trim() || shot.audio_path)
              }
              onClick={() =>
                void runShotLipSync({
                  shot,
                  videoPath: lipVideoOverride || undefined,
                  audioPath: lipAudioOverride || undefined,
                  modelRef: lipModelRef || undefined,
                  faceIndex: lipFaceIndex,
                  prompt: lipPrompt || undefined,
                })
              }
            >
              {t("studio.video.runLipSync")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
};

export const VideoShotsStagePanel: React.FC<{
  detail: StudioEpisodeDetailResult;
  videoModel: string;
  ttsModel: string;
}> = ({ detail, videoModel, ttsModel }) => {
  const t = useT();
  const videoSpec = useNodeSpec(NodeTypes.VIDEO_GENERATE);
  const resParam = findParam(videoSpec, "resolution");
  const resOptions = resParam?.options ?? [];
  const [resolution, setResolution] = useState("");
  const effectiveResolution = resolution || resParam?.default || resOptions[0] || "";

  const runStage = useStudioStore((s) => s.runStage);
  const enqueueBatch = useStudioStore((s) => s.enqueueBatch);
  const extractLastFrame = useStudioStore((s) => s.extractLastFrame);
  const openVersionDrawer = useStudioStore((s) => s.openVersionDrawer);
  const qcByTarget = useStudioStore((s) => s.qcByTarget);
  const probeShot = useStudioStore((s) => s.probeShot);
  const probeMap = useStudioStore((s) => s.probeMap);
  const runningStage = useStudioStore((s) => s.runningStage);
  const saving = useStudioStore((s) => s.saving);
  const busy = saving || runningStage !== null;

  const renderProbeBadge = (probe?: ProbeMediaOutput) => {
    if (!probe) return null;
    const dur = probe.duration_sec ?? 0;
    const width = probe.width ?? 0;
    const height = probe.height ?? 0;
    const fps = probe.fps ?? 0;
    const silentRatio = probe.silent_ratio ?? 0;
    const isSilent = !probe.has_audio || silentRatio > appConfig.studio.ui.probeSilenceWarnRatio;
    return (
      <div className="studio-toolbar">
        <Badge>
          {t("studio.video.probeMeta", {
            duration: dur.toFixed(1),
            width,
            height,
            fps: Math.round(fps),
          })}
        </Badge>
        {isSilent ? (
          <Badge className="studio-badge--warn">
            {t("studio.video.silentWarn", { ratio: Math.round(silentRatio * 100) })}
          </Badge>
        ) : (
          <Badge className="studio-badge--ok">{t("studio.video.hasAudio")}</Badge>
        )}
      </div>
    );
  };

  return (
    <div className="studio-form-stack">
      <div className="studio-stage-header">
        <h2 className="card-title">{t("studio.video.title")}</h2>
        <div className="studio-toolbar">
          <select
            className="input-control"
            value={effectiveResolution}
            onChange={(e) => setResolution(e.target.value)}
            aria-label={t("studio.video.resolution")}
            disabled={busy}
          >
            {resOptions.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void runStage("generate_tts", ttsModel ? { model: ttsModel } : {})}
            disabled={busy || detail.shots.length === 0}
          >
            {runningStage === "generate_tts" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Volume2 size={13} />
            )}
            {t("studio.video.generateTTS")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() =>
              void runStage("generate_videos", {
                resolution: effectiveResolution,
                ...(videoModel ? { model: videoModel } : {}),
              })
            }
            disabled={busy || detail.shots.length === 0}
          >
            {runningStage === "generate_videos" ? (
              <Loader2 size={13} className="animate-spin" />
            ) : (
              <Film size={13} />
            )}
            {t("studio.video.generateAll")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void runStage("probe_shots")}
            disabled={busy || !detail.shots.some((s) => Boolean(s.video_path))}
          >
            {t("studio.video.probeAll")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() =>
              void enqueueBatch({
                stage: "generate_videos",
                ...(videoModel ? { model: videoModel } : {}),
              })
            }
            disabled={busy || detail.shots.length === 0}
          >
            <Play size={13} />
            {t("studio.queue.enqueueBatch")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void enqueueBatch({ stage: "lipsync" })}
            disabled={busy || !detail.shots.some((s) => Boolean(s.video_path && s.audio_path))}
          >
            <Speech size={13} />
            {t("studio.voice.lipSyncButton")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              void enqueueBatch({
                stage: "tts_dub_mux",
                ...(ttsModel ? { model: ttsModel } : {}),
              })
            }
            disabled={
              busy ||
              !detail.shots.some((s) =>
                Boolean(s.video_path && (s.audio_path || s.narration_text || s.dialogues?.length))
              )
            }
          >
            <Volume2 size={13} />
            {t("studio.video.batchDubMux")}
          </Button>
        </div>
      </div>

      {detail.shots.length === 0 ? (
        <div className="studio-empty-state">
          <Film size={28} />
          <p>{t("studio.video.empty")}</p>
        </div>
      ) : (
        <div className="studio-card-grid">
          {detail.shots.map((shot, idx) => {
            const probe = probeMap[shot.id];
            const nextShot = detail.shots[idx + 1];
            const latestQC = qcByTarget[targetVersionKey("video", shot.id)];
            return (
              <div key={shot.id} className="studio-item-card">
                <div className="studio-item-head">
                  <div className="studio-item-head-left">
                    <Badge>#{shot.seq}</Badge>
                    <strong>{shot.title}</strong>
                  </div>
                  <div className="studio-phase2-meta-grid">
                    {latestQC && (
                      <Badge
                        className={
                          latestQC.verdict === "pass"
                            ? "studio-badge--ok"
                            : latestQC.verdict === "fail"
                              ? "studio-badge--error"
                              : "studio-badge--warn"
                        }
                      >
                        {t(`studio.qc.verdict.${latestQC.verdict}` as never)}
                      </Badge>
                    )}
                    <Badge>{t(`studio.shotStatus.${shot.status}`)}</Badge>
                  </div>
                </div>

                <div className="studio-media-frame">
                  <SignedStudioVideo
                    assetId={shot.video_asset_id}
                    placeholder={t("studio.video.noClip")}
                  />
                </div>

                <SignedStudioAudio assetId={shot.audio_asset_id} />
                {renderProbeBadge(probe)}

                {shot.narration_text && <p>{shot.narration_text}</p>}

                <div className="studio-toolbar">
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() =>
                      void runStage("generate_videos", {
                        shot_ids: [shot.id],
                        resolution: effectiveResolution,
                        force: true,
                        ...(videoModel ? { model: videoModel } : {}),
                      })
                    }
                    disabled={busy}
                  >
                    <Film size={13} />
                    {t("studio.video.genClip")}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() =>
                      void runStage("generate_tts", {
                        shot_ids: [shot.id],
                        force: true,
                        ...(ttsModel ? { model: ttsModel } : {}),
                      })
                    }
                    disabled={busy || (!shot.narration_text && !shot.dialogues?.length)}
                  >
                    <Volume2 size={13} />
                    {t("studio.video.genTTS")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      openVersionDrawer({
                        projectId: detail.project.id,
                        episodeId: detail.episode.id,
                        targetKind: "video",
                        targetId: shot.id,
                        label: shot.title,
                        mediaPath: shot.video_path,
                        mediaAssetId: shot.video_asset_id,
                        promptExpected: shot.video_prompt,
                        referencePaths: shot.keyframe_path ? [shot.keyframe_path] : undefined,
                      })
                    }
                    disabled={busy}
                  >
                    <Layers size={13} />
                    {t("studio.versions.button")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      if (nextShot) {
                        void extractLastFrame({
                          source_shot_id: shot.id,
                          target_shot_id: nextShot.id,
                        });
                      }
                    }}
                    disabled={busy || !shot.video_path || !nextShot}
                    title={
                      nextShot
                        ? t("studio.extractFrame.button")
                        : t("studio.extractFrame.noNextShot")
                    }
                  >
                    <ImageIcon size={13} />
                    {t("studio.extractFrame.button")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => void probeShot(shot)}
                    disabled={busy || !shot.video_path}
                  >
                    {t("studio.video.probe")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      askAgentAboutMediaAsset({
                        assetId: shot.video_asset_id || undefined,
                        filePath: shot.video_path || undefined,
                        fileName: shot.title,
                      })
                    }
                    title={t("studio.common.askAgent")}
                  >
                    <Bot size={13} />
                  </Button>
                </div>

                <ShotPostToolsPanel shot={shot} ttsModel={ttsModel} busy={busy} />
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};
