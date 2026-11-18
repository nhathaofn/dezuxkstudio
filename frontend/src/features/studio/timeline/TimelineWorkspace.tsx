import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Bot,
  Clapperboard,
  Download,
  FileArchive,
  Film,
  FolderOpen,
  Loader2,
  Pause,
  Play,
  Plus,
  Redo2,
  RefreshCw,
  Scissors,
  SkipBack,
  SkipForward,
  Sparkles,
  Trash2,
  Undo2,
  Volume2,
  Waves,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Badge } from "../../../components/ui/Badge";
import { Button } from "../../../components/ui/Button";
import { Card } from "../../../components/ui/Card";
import { Dialog } from "../../../components/ui/Dialog";
import { Switch } from "../../../components/ui/Switch";
import { appConfig } from "../../../config/appConfig";
import {
  NodeTypes,
  StudioKenBurnsPresetValues,
  StudioTransitionTypeValues,
  type Param,
  type StudioEditTimeline,
  type StudioEpisodeDetailResult,
  type StudioKenBurnsPreset,
  type StudioNLEExportFormat,
  type StudioSubtitleCue,
  type StudioTimelineClip,
  type StudioTimelineOp,
  type StudioTimelineOpKind,
  type StudioTrackKind,
  type StudioTransitionType,
} from "../../../contracts/generated";
import { useSignedMediaURLs } from "../../../lib/hooks/useSignedMediaURL";
import { isMessageKey, translateServerMessage, useT } from "../../../lib/i18n";
import { mediaBasename, saveMediaToDevice } from "../../../lib/rtClient";
import { askAgentAboutMediaAsset } from "../../agent/screenContext";
import { findParam, useNodeSpec } from "../../workflow/useNodeSpecs";
import { SignedStudioVideo } from "../SignedStudioMedia";
import { useStudioStore } from "../useStudioStore";
import {
  buildClipTrimOp,
  clipEndMs,
  clipSpanMs,
  collectTimelineAssetIds,
  computeTimelineDurationMs,
  findClipInTimeline,
  formatTimecodeMs,
  isAudioTrackKind,
  resolvePreviewStateAtTime,
} from "./timelineHelpers";
import "./TimelineWorkspace.css";

interface RenderDialogProps {
  defaultFilename: string;
  onClose: () => void;
}

function parseOptionalParamNumber(param: Param | undefined): number | undefined {
  if (param?.default === undefined || param.default === "") return undefined;
  const n = Number(param.default);
  return Number.isFinite(n) ? n : undefined;
}

export const RenderTimelineDialog: React.FC<RenderDialogProps> = ({ defaultFilename, onClose }) => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.RENDER_TIMELINE);
  const renderTimeline = useStudioStore((s) => s.renderTimeline);
  const rendering = useStudioStore((s) => s.renderingTimeline);

  const fontParam = findParam(spec, "subtitle_font_size");
  const lufsParam = findParam(spec, "loudnorm_lufs");
  const crfParam = findParam(spec, "crf");
  const presetParam = findParam(spec, "preset");
  const burnParam = findParam(spec, "burn_subtitles");
  const overwriteParam = findParam(spec, "overwrite");

  const [filename, setFilename] = useState(defaultFilename);
  const [burnSubtitlesOverride, setBurnSubtitlesOverride] = useState<boolean | null>(null);
  const [overwriteOverride, setOverwriteOverride] = useState<boolean | null>(null);
  const [fontSizeOverride, setFontSizeOverride] = useState<number | null>(null);
  const [lufsOverride, setLufsOverride] = useState<number | null>(null);
  const [crfOverride, setCrfOverride] = useState<number | null>(null);
  const [presetOverride, setPresetOverride] = useState<string | null>(null);

  const burnSubtitles = burnSubtitlesOverride ?? burnParam?.default === "true";
  const overwrite = overwriteOverride ?? overwriteParam?.default === "true";
  const fontSize = fontSizeOverride ?? parseOptionalParamNumber(fontParam);
  const loudnormLUFS = lufsOverride ?? parseOptionalParamNumber(lufsParam);
  const crf = crfOverride ?? parseOptionalParamNumber(crfParam);
  const presetOptions = presetParam?.options ?? [];
  const preset = presetOverride ?? (presetParam?.default || presetOptions[0] || "");

  const submit = async () => {
    const res = await renderTimeline({
      filename: filename.trim() || undefined,
      burn_subtitles: burnSubtitles,
      overwrite,
      ...(fontSize !== undefined ? { subtitle_font_size: fontSize } : {}),
      ...(loudnormLUFS !== undefined ? { loudnorm_lufs: loudnormLUFS } : {}),
      ...(crf !== undefined ? { crf } : {}),
      ...(preset ? { preset } : {}),
    });
    if (res) onClose();
  };

  return (
    <Dialog
      title={t("studio.timeline.renderDialogTitle")}
      onClose={onClose}
      busy={rendering}
      footer={
        <div className="studio-toolbar">
          <Button variant="ghost" onClick={onClose} disabled={rendering}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={() => void submit()} disabled={rendering}>
            {rendering ? <Loader2 size={14} className="animate-spin" /> : <Film size={14} />}
            {t("studio.timeline.startRender")}
          </Button>
        </div>
      }
    >
      <div className="studio-form-stack">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-render-filename">
            {t("studio.timeline.filename")}
          </label>
          <input
            id="tl-render-filename"
            className="input-control"
            value={filename}
            onChange={(e) => setFilename(e.target.value)}
            disabled={rendering}
          />
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-lufs">
              {t("studio.timeline.loudnormLufs")}
            </label>
            <input
              id="tl-render-lufs"
              type="number"
              step={appConfig.studio.timeline.loudnormStepLufs}
              min={lufsParam?.min}
              max={lufsParam?.max}
              className="input-control"
              value={loudnormLUFS ?? ""}
              onChange={(e) =>
                setLufsOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-fontsize">
              {t("studio.timeline.subtitleFontSize")}
            </label>
            <input
              id="tl-render-fontsize"
              type="number"
              min={fontParam?.min}
              max={fontParam?.max}
              className="input-control"
              value={fontSize ?? ""}
              onChange={(e) =>
                setFontSizeOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>
        </div>

        <div className="studio-field-row">
          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-crf">
              {t("studio.timeline.crf")}
            </label>
            <input
              id="tl-render-crf"
              type="number"
              min={crfParam?.min}
              max={crfParam?.max}
              className="input-control"
              value={crf ?? ""}
              onChange={(e) =>
                setCrfOverride(e.target.value === "" ? null : Number(e.target.value))
              }
              disabled={rendering}
            />
          </div>

          <div className="form-group">
            <label className="field-label" htmlFor="tl-render-preset">
              {t("studio.timeline.preset")}
            </label>
            <select
              id="tl-render-preset"
              className="input-control"
              value={preset}
              onChange={(e) => setPresetOverride(e.target.value)}
              disabled={rendering}
            >
              {presetOptions.map((opt) => {
                const presetKey = `studio.timeline.presets.${opt}`;
                return (
                  <option key={opt} value={opt}>
                    {isMessageKey(presetKey) ? t(presetKey) : opt}
                  </option>
                );
              })}
            </select>
          </div>
        </div>

        <label className="studio-inline-toggle">
          <Switch
            checked={burnSubtitles}
            onCheckedChange={setBurnSubtitlesOverride}
            aria-label={t("studio.timeline.burnSubtitles")}
            disabled={rendering}
          />
          <span>{t("studio.timeline.burnSubtitles")}</span>
        </label>

        <label className="studio-inline-toggle">
          <Switch
            checked={overwrite}
            onCheckedChange={setOverwriteOverride}
            aria-label={t("studio.timeline.overwriteOutput")}
            disabled={rendering}
          />
          <span>{t("studio.timeline.overwriteOutput")}</span>
        </label>
      </div>
    </Dialog>
  );
};

interface ExportNLEDialogProps {
  defaultProjectName: string;
  onClose: () => void;
}

export const ExportNLEDraftDialog: React.FC<ExportNLEDialogProps> = ({
  defaultProjectName,
  onClose,
}) => {
  const t = useT();
  const spec = useNodeSpec(NodeTypes.EXPORT_NLE_DRAFT);
  const exportNLEDraft = useStudioStore((s) => s.exportNLEDraft);
  const exporting = useStudioStore((s) => s.exportingNLE);

  const formatParam = findParam(spec, "format");
  const includeParam = findParam(spec, "include_media_copy");
  const formatOptions = (formatParam?.options ?? []) as StudioNLEExportFormat[];

  const [formatOverride, setFormatOverride] = useState<StudioNLEExportFormat | null>(null);
  const [projectName, setProjectName] = useState(defaultProjectName);
  const [includeMediaOverride, setIncludeMediaOverride] = useState<boolean | null>(null);

  const format =
    formatOverride ?? ((formatParam?.default || formatOptions[0] || "") as StudioNLEExportFormat);
  const includeMedia =
    includeMediaOverride ?? (includeParam?.default ? includeParam.default === "true" : false);

  const submit = async () => {
    if (!format) return;
    const out = await exportNLEDraft({
      format,
      project_name: projectName.trim() || undefined,
      include_media_copy: includeMedia,
    });
    if (out) onClose();
  };

  return (
    <Dialog
      title={t("studio.timeline.exportNLETitle")}
      onClose={onClose}
      busy={exporting}
      footer={
        <div className="studio-toolbar">
          <Button variant="ghost" onClick={onClose} disabled={exporting}>
            {t("studio.modal.cancel")}
          </Button>
          <Button variant="primary" onClick={() => void submit()} disabled={exporting || !format}>
            {exporting ? <Loader2 size={14} className="animate-spin" /> : <FileArchive size={14} />}
            {t("studio.timeline.startExportNLE")}
          </Button>
        </div>
      }
    >
      <div className="studio-form-stack">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-nle-format">
            {t("studio.timeline.nleFormat")}
          </label>
          <select
            id="tl-nle-format"
            className="input-control"
            value={format}
            onChange={(e) => setFormatOverride(e.target.value as StudioNLEExportFormat)}
            disabled={exporting}
          >
            {formatOptions.map((fmt) => {
              const fmtKey = `studio.timeline.nleFormats.${fmt}`;
              return (
                <option key={fmt} value={fmt}>
                  {isMessageKey(fmtKey) ? t(fmtKey) : fmt}
                </option>
              );
            })}
          </select>
        </div>

        <div className="form-group">
          <label className="field-label" htmlFor="tl-nle-name">
            {t("studio.timeline.nleProjectName")}
          </label>
          <input
            id="tl-nle-name"
            className="input-control"
            value={projectName}
            onChange={(e) => setProjectName(e.target.value)}
            disabled={exporting}
          />
        </div>

        {(format === "capcut" || format === "jianying") && (
          <label className="studio-inline-toggle">
            <Switch
              checked={includeMedia}
              onCheckedChange={setIncludeMediaOverride}
              aria-label={t("studio.timeline.includeMediaCopy")}
              disabled={exporting}
            />
            <span>{t("studio.timeline.includeMediaCopy")}</span>
          </label>
        )}
      </div>
    </Dialog>
  );
};

interface ClipInspectorProps {
  clip: StudioTimelineClip;
  locked: boolean;
  busy: boolean;
  canSplit: boolean;
  playheadMs: number;
  loadingPeaks: boolean;
  onApplyOps: (ops: StudioTimelineOp[], summary?: string) => Promise<boolean>;
  onComputePeaks: (mediaPath: string, clipId?: string) => Promise<unknown>;
  onDeleted: () => void;
}

export const ClipInspectorForm: React.FC<ClipInspectorProps> = ({
  clip,
  locked,
  busy,
  canSplit,
  playheadMs,
  loadingPeaks,
  onApplyOps,
  onComputePeaks,
  onDeleted,
}) => {
  const t = useT();
  const assembleSpec = useNodeSpec(NodeTypes.TIMELINE_AUTO_ASSEMBLE);
  const transMsParam = findParam(assembleSpec, "transition_ms");
  const trimStepSec = (appConfig.studio.timeline.trimStepMs / 1000).toFixed(1);

  const [label, setLabel] = useState(clip.label ?? "");
  const [speed, setSpeed] = useState(clip.speed || 1);
  const [volume, setVolume] = useState(clip.volume ?? 1);
  const [muted, setMuted] = useState(Boolean(clip.mute));
  const [transitionIn, setTransitionIn] = useState<StudioTransitionType>(
    clip.transition_in || "none"
  );
  const [transitionMs, setTransitionMs] = useState(clip.transition_ms || 0);
  const [kenBurns, setKenBurns] = useState<StudioKenBurnsPreset>(clip.ken_burns_preset || "none");

  const commitEdits = () => {
    void onApplyOps(
      [
        {
          op: "update_clip" satisfies StudioTimelineOpKind,
          clip_id: clip.id,
          label,
          speed,
          volume,
          muted,
          transition_in: transitionIn,
          transition_ms: transitionMs,
          ken_burns_preset: kenBurns,
        },
      ],
      t("studio.timeline.op.updateClip", { name: clip.label || clip.id })
    );
  };

  return (
    <>
      <div className="form-group">
        <label className="field-label" htmlFor="tl-clip-label">
          {t("studio.timeline.clipLabel")}
        </label>
        <input
          id="tl-clip-label"
          className="input-control"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          disabled={busy || locked}
        />
      </div>

      <div className="studio-toolbar">
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || locked}
          onClick={() => {
            const op = buildClipTrimOp(clip, "in", -appConfig.studio.timeline.trimStepMs);
            if (op) void onApplyOps([op], t("studio.timeline.op.trimIn"));
          }}
        >
          {t("studio.timeline.trimInEarlier", { sec: trimStepSec })}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || locked}
          onClick={() => {
            const op = buildClipTrimOp(clip, "in", appConfig.studio.timeline.trimStepMs);
            if (op) void onApplyOps([op], t("studio.timeline.op.trimIn"));
          }}
        >
          {t("studio.timeline.trimInLater", { sec: trimStepSec })}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || locked}
          onClick={() => {
            const op = buildClipTrimOp(clip, "out", -appConfig.studio.timeline.trimStepMs);
            if (op) void onApplyOps([op], t("studio.timeline.op.trimOut"));
          }}
        >
          {t("studio.timeline.trimOutEarlier", { sec: trimStepSec })}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || locked}
          onClick={() => {
            const op = buildClipTrimOp(clip, "out", appConfig.studio.timeline.trimStepMs);
            if (op) void onApplyOps([op], t("studio.timeline.op.trimOut"));
          }}
        >
          {t("studio.timeline.trimOutLater", { sec: trimStepSec })}
        </Button>
      </div>

      <div className="studio-field-row">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-speed">
            {t("studio.timeline.speed")}
          </label>
          <input
            id="tl-clip-speed"
            type="number"
            step={appConfig.studio.timeline.speedStep}
            min={appConfig.studio.timeline.minSpeed}
            max={appConfig.studio.timeline.maxSpeed}
            className="input-control"
            value={speed}
            onChange={(e) => setSpeed(Number(e.target.value) || 1)}
            disabled={busy || locked}
          />
        </div>
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-volume">
            {t("studio.timeline.volume")}
          </label>
          <input
            id="tl-clip-volume"
            type="number"
            step={appConfig.studio.timeline.volumeStep}
            min={0}
            max={appConfig.studio.timeline.maxVolume}
            className="input-control"
            value={volume}
            onChange={(e) => setVolume(Number(e.target.value))}
            disabled={busy || locked}
          />
        </div>
      </div>

      <label className="studio-inline-toggle">
        <Switch
          checked={muted}
          onCheckedChange={setMuted}
          aria-label={t("studio.timeline.clipMute")}
          disabled={busy || locked}
        />
        <span>{t("studio.timeline.clipMute")}</span>
      </label>

      <div className="studio-field-row">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-trans">
            {t("studio.timeline.transitionIn")}
          </label>
          <select
            id="tl-clip-trans"
            className="input-control"
            value={transitionIn}
            onChange={(e) => setTransitionIn(e.target.value as StudioTransitionType)}
            disabled={busy || locked}
          >
            {StudioTransitionTypeValues.map((trType) => (
              <option key={trType} value={trType}>
                {t(`studio.timeline.transitions.${trType}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-transms">
            {t("studio.timeline.transitionMs")}
          </label>
          <input
            id="tl-clip-transms"
            type="number"
            step={appConfig.studio.timeline.transitionStepMs}
            min={transMsParam?.min ?? 0}
            max={transMsParam?.max}
            className="input-control"
            value={transitionMs}
            onChange={(e) => setTransitionMs(Number(e.target.value) || 0)}
            disabled={busy || locked}
          />
        </div>
      </div>

      <div className="form-group">
        <label className="field-label" htmlFor="tl-clip-kb">
          {t("studio.timeline.kenBurns")}
        </label>
        <select
          id="tl-clip-kb"
          className="input-control"
          value={kenBurns}
          onChange={(e) => setKenBurns(e.target.value as StudioKenBurnsPreset)}
          disabled={busy || locked}
        >
          {StudioKenBurnsPresetValues.map((kb) => (
            <option key={kb} value={kb}>
              {t(`studio.timeline.kenBurnsPresets.${kb}`)}
            </option>
          ))}
        </select>
      </div>

      <div className="studio-toolbar">
        <Button variant="primary" size="sm" disabled={busy || locked} onClick={commitEdits}>
          {t("studio.timeline.applyClipChanges")}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || !canSplit}
          onClick={() =>
            void onApplyOps(
              [
                {
                  op: "split_clip" satisfies StudioTimelineOpKind,
                  clip_id: clip.id,
                  split_at_ms: Math.round(playheadMs),
                },
              ],
              t("studio.timeline.op.splitClip")
            )
          }
        >
          <Scissors size={14} />
          {t("studio.timeline.splitAtPlayhead")}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || !clip.file_path || loadingPeaks}
          onClick={() => void onComputePeaks(clip.file_path, clip.id)}
        >
          {loadingPeaks ? <Loader2 size={14} className="animate-spin" /> : <Waves size={14} />}
          {t("studio.timeline.extractWaveformThumbs")}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          disabled={busy || locked}
          onClick={() => {
            const id = clip.id;
            const name = clip.label || clip.id;
            onDeleted();
            void onApplyOps(
              [{ op: "remove_clip" satisfies StudioTimelineOpKind, clip_id: id }],
              t("studio.timeline.op.removeClip", { name })
            );
          }}
        >
          <Trash2 size={14} />
          {t("studio.timeline.deleteClip")}
        </Button>
      </div>
    </>
  );
};

export const TimelineWorkspace: React.FC<{ detail: StudioEpisodeDetailResult }> = ({ detail }) => {
  const t = useT();
  const timelineResult = useStudioStore((s) => s.timelineResult);
  const timelineLoading = useStudioStore((s) => s.timelineLoading);
  const saving = useStudioStore((s) => s.saving);
  const runningStage = useStudioStore((s) => s.runningStage);
  const renderingTimeline = useStudioStore((s) => s.renderingTimeline);
  const exportingNLE = useStudioStore((s) => s.exportingNLE);
  const actionProgress = useStudioStore((s) => s.actionProgress);
  const lastRenderResult = useStudioStore((s) => s.lastRenderResult);
  const lastNLEExport = useStudioStore((s) => s.lastNLEExport);
  const peaksLoadingClipIds = useStudioStore((s) => s.peaksLoadingClipIds);

  const fetchTimeline = useStudioStore((s) => s.fetchTimeline);
  const applyTimelineOps = useStudioStore((s) => s.applyTimelineOps);
  const undoTimeline = useStudioStore((s) => s.undoTimeline);
  const redoTimeline = useStudioStore((s) => s.redoTimeline);
  const autoAssembleTimeline = useStudioStore((s) => s.autoAssembleTimeline);
  const computeClipPeaksThumbs = useStudioStore((s) => s.computeClipPeaksThumbs);
  const openExportFolder = useStudioStore((s) => s.openExportFolder);

  const epId = detail.episode.id;
  const readyShotsCount = detail.shots.filter((sh) => Boolean(sh.video_path)).length;

  useEffect(() => {
    if (!timelineResult || timelineResult.episode_id !== epId) {
      void fetchTimeline(epId);
    }
  }, [epId, timelineResult, fetchTimeline]);

  const timeline: StudioEditTimeline | null =
    timelineResult?.episode_id === epId ? timelineResult.timeline : null;
  const durationMs = Math.max(
    appConfig.studio.timeline.minDurationMs,
    computeTimelineDurationMs(timeline)
  );

  const [playheadMs, setPlayheadMs] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [pxPerSec, setPxPerSec] = useState<number>(appConfig.studio.timeline.defaultPxPerSec);
  const [selectedClipId, setSelectedClipId] = useState<string | null>(null);
  const [showRenderDialog, setShowRenderDialog] = useState(false);
  const [showNLEDialog, setShowNLEDialog] = useState(false);

  const clampedPlayheadMs = Math.min(playheadMs, durationMs);

  // Subtitle creation / inline editing state
  const [newSubText, setNewSubText] = useState("");
  const [newSubSpeaker, setNewSubSpeaker] = useState("");
  const [editingSub, setEditingSub] = useState<StudioSubtitleCue | null>(null);

  // Selected clip derived from timeline state
  const selectedEntry = useMemo(
    () => findClipInTimeline(timeline, selectedClipId),
    [timeline, selectedClipId]
  );

  const timelineAssetIds = useMemo(() => collectTimelineAssetIds(timeline), [timeline]);
  const { urls: signedUrls, handleAssetMediaError } = useSignedMediaURLs(
    timelineAssetIds,
    "preview"
  );

  // Playback refs & rAF-synchronized <video> + <audio> elements
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const audioRefs = useRef<Record<string, HTMLAudioElement | null>>({});
  const playheadRef = useRef(clampedPlayheadMs);

  useEffect(() => {
    playheadRef.current = clampedPlayheadMs;
  }, [clampedPlayheadMs]);

  const previewState = useMemo(
    () => resolvePreviewStateAtTime(timeline, clampedPlayheadMs),
    [timeline, clampedPlayheadMs]
  );

  const activeAudioTracks = useMemo(
    () => (timeline?.tracks ?? []).filter((tr) => isAudioTrackKind(tr.kind)),
    [timeline]
  );

  const syncMediaElements = useCallback(
    (targetMs: number, isPlaying: boolean) => {
      const state = resolvePreviewStateAtTime(timeline, targetMs);
      const vid = videoRef.current;
      if (vid && state.videoClip?.asset_id && signedUrls[state.videoClip.asset_id]) {
        vid.muted = state.videoTrackMuted;
        vid.playbackRate = state.videoClip.speed > 0 ? state.videoClip.speed : 1;
        if (
          Math.abs(vid.currentTime - state.videoSourceSec) >
          appConfig.studio.timeline.playSeekToleranceSec
        ) {
          try {
            vid.currentTime = Math.max(0, state.videoSourceSec);
          } catch {
            // Element metadata may still be loading
          }
        }
        if (isPlaying && vid.paused) {
          void vid.play().catch(() => {});
        } else if (!isPlaying && !vid.paused) {
          vid.pause();
        }
      } else if (vid && !vid.paused) {
        vid.pause();
      }

      const cueByTrack = new Map(state.audioCues.map((c) => [c.trackId, c]));
      for (const tr of activeAudioTracks) {
        const ael = audioRefs.current[tr.id];
        if (!ael) continue;
        const cue = cueByTrack.get(tr.id);
        if (cue && cue.clip.asset_id && signedUrls[cue.clip.asset_id]) {
          ael.volume = Math.max(0, Math.min(1, cue.effectiveVolume));
          ael.playbackRate = cue.clip.speed > 0 ? cue.clip.speed : 1;
          if (
            Math.abs(ael.currentTime - cue.sourceSec) >
            appConfig.studio.timeline.playSeekToleranceSec
          ) {
            try {
              ael.currentTime = Math.max(0, cue.sourceSec);
            } catch {
              // Audio metadata loading
            }
          }
          if (isPlaying && ael.paused) {
            void ael.play().catch(() => {});
          } else if (!isPlaying && !ael.paused) {
            ael.pause();
          }
        } else if (!ael.paused) {
          ael.pause();
        }
      }
    },
    [timeline, signedUrls, activeAudioTracks]
  );

  const setVideoRef = useCallback((el: HTMLVideoElement | null) => {
    if (!el) {
      videoRef.current?.pause();
    }
    videoRef.current = el;
  }, []);

  useEffect(() => {
    if (!playing) {
      syncMediaElements(clampedPlayheadMs, false);
    }
  }, [playing, clampedPlayheadMs, syncMediaElements]);

  useEffect(() => {
    if (!playing) return;
    let rafId = 0;
    let lastTs = performance.now();

    const tick = (now: number) => {
      const dt = Math.max(0, now - lastTs);
      lastTs = now;
      const nextMs = Math.min(durationMs, playheadRef.current + dt);
      playheadRef.current = nextMs;
      setPlayheadMs(Math.round(nextMs));
      syncMediaElements(nextMs, true);
      if (nextMs >= durationMs) {
        setPlaying(false);
        return;
      }
      rafId = requestAnimationFrame(tick);
    };

    rafId = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(rafId);
  }, [playing, durationMs, syncMediaElements]);

  useEffect(() => {
    const audioMap = audioRefs.current;
    return () => {
      const vid = videoRef.current;
      if (vid) {
        vid.pause();
        vid.removeAttribute("src");
        vid.load?.();
      }
      for (const el of Object.values(audioMap)) {
        if (el) {
          el.pause();
          el.removeAttribute("src");
          el.load?.();
        }
      }
    };
  }, []);

  const seekTo = (ms: number) => {
    const clamped = Math.max(0, Math.min(durationMs, Math.round(ms)));
    playheadRef.current = clamped;
    setPlayheadMs(clamped);
    syncMediaElements(clamped, playing);
  };

  const laneWidthPx = Math.max(
    appConfig.studio.timeline.minLaneWidthPx,
    Math.ceil((durationMs / 1000) * pxPerSec) + appConfig.studio.timeline.lanePaddingPx
  );
  const rulerTicks = useMemo(() => {
    const {
      rulerMediumThresholdPx,
      rulerDenseThresholdPx,
      rulerStepCoarseSec,
      rulerStepMediumSec,
      rulerStepFineSec,
    } = appConfig.studio.timeline;
    const stepSec =
      pxPerSec < rulerMediumThresholdPx
        ? rulerStepCoarseSec
        : pxPerSec < rulerDenseThresholdPx
          ? rulerStepMediumSec
          : rulerStepFineSec;
    const totalSec = Math.ceil(durationMs / 1000);
    const out: number[] = [];
    for (let s = 0; s <= totalSec; s += stepSec) {
      out.push(s);
    }
    return out;
  }, [durationMs, pxPerSec]);

  const busy = saving || timelineLoading || renderingTimeline || exportingNLE;
  const activeVideoSrc = previewState.videoClip?.asset_id
    ? signedUrls[previewState.videoClip.asset_id]
    : undefined;

  const canSplitSelected = Boolean(
    selectedEntry &&
    !selectedEntry.track.locked &&
    clampedPlayheadMs >
      selectedEntry.clip.timeline_start_ms + appConfig.studio.timeline.splitGuardEdgeMs &&
    clampedPlayheadMs < clipEndMs(selectedEntry.clip) - appConfig.studio.timeline.splitGuardEdgeMs
  );

  const addSubtitleCue = async () => {
    if (!newSubText.trim()) return;
    const startMs = Math.max(0, Math.round(clampedPlayheadMs));
    const endMs = startMs + appConfig.studio.timeline.subtitleDefaultDurMs;
    const cueId =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? `sub-${crypto.randomUUID()}`
        : `sub-${Date.now()}`;
    await applyTimelineOps(
      [
        {
          op: "add_subtitle" satisfies StudioTimelineOpKind,
          subtitle: {
            id: cueId,
            start_ms: startMs,
            end_ms: endMs,
            speaker: newSubSpeaker.trim() || undefined,
            text: newSubText.trim(),
          },
        },
      ],
      t("studio.timeline.op.addSubtitle")
    );
    setNewSubText("");
    setNewSubSpeaker("");
  };

  const saveSubtitleEdit = async () => {
    if (!editingSub || !editingSub.text.trim()) return;
    await applyTimelineOps(
      [
        {
          op: "update_subtitle" satisfies StudioTimelineOpKind,
          subtitle_id: editingSub.id,
          subtitle: editingSub,
        },
      ],
      t("studio.timeline.op.updateSubtitle")
    );
    setEditingSub(null);
  };

  const finalVideoPath = lastRenderResult?.output_path ?? detail.final_video_path;
  const finalVideoAssetId = lastRenderResult?.asset_id ?? detail.final_video_asset_id;
  const activeAudioCueByTrack = useMemo(
    () => new Map(previewState.audioCues.map((c) => [c.trackId, c])),
    [previewState.audioCues]
  );

  return (
    <div className="timeline-workspace">
      {/* Top Toolbar */}
      <div className="timeline-toolbar-card">
        <div className="timeline-toolbar-group">
          <Badge>
            {t("studio.timeline.revisionBadge", {
              rev: timelineResult?.revision ?? 0,
            })}
          </Badge>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void undoTimeline()}
            disabled={busy || !timelineResult?.can_undo}
            title={t("studio.timeline.undo")}
          >
            <Undo2 size={14} />
            {t("studio.timeline.undo")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void redoTimeline()}
            disabled={busy || !timelineResult?.can_redo}
            title={t("studio.timeline.redo")}
          >
            <Redo2 size={14} />
            {t("studio.timeline.redo")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void autoAssembleTimeline()}
            disabled={busy || readyShotsCount === 0}
          >
            {timelineLoading ? (
              <Loader2 size={14} className="animate-spin" />
            ) : (
              <Sparkles size={14} />
            )}
            {t("studio.timeline.autoAssemble")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void fetchTimeline(epId)}
            disabled={busy}
            title={t("studio.timeline.refreshTimeline")}
            aria-label={t("studio.timeline.refreshTimeline")}
          >
            <RefreshCw size={14} />
          </Button>
        </div>

        <div className="timeline-toolbar-group">
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              setPxPerSec((z) =>
                Math.max(
                  appConfig.studio.timeline.minPxPerSec,
                  z - appConfig.studio.timeline.zoomStepPx
                )
              )
            }
            title={t("studio.timeline.zoomOut")}
            aria-label={t("studio.timeline.zoomOut")}
          >
            <ZoomOut size={14} />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              setPxPerSec((z) =>
                Math.min(
                  appConfig.studio.timeline.maxPxPerSec,
                  z + appConfig.studio.timeline.zoomStepPx
                )
              )
            }
            title={t("studio.timeline.zoomIn")}
            aria-label={t("studio.timeline.zoomIn")}
          >
            <ZoomIn size={14} />
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setShowNLEDialog(true)}
            disabled={
              busy ||
              !timeline ||
              (timeline.tracks ?? []).every((tr) => (tr.clips ?? []).length === 0)
            }
          >
            <FileArchive size={14} />
            {t("studio.timeline.exportNLE")}
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => setShowRenderDialog(true)}
            disabled={
              busy ||
              !timeline ||
              (timeline.tracks ?? []).every((tr) => (tr.clips ?? []).length === 0)
            }
          >
            <Clapperboard size={14} />
            {t("studio.timeline.renderMP4")}
          </Button>
        </div>
      </div>

      {actionProgress && runningStage === null && (
        <div className="studio-progress-banner">
          <div className="studio-item-head">
            <span className="studio-toolbar">
              <Loader2 size={14} className="animate-spin" />
              <span>
                {translateServerMessage(t, actionProgress.msg_key, actionProgress.msg_params) ??
                  actionProgress.message}
              </span>
            </span>
            {actionProgress.percent > 0 && <Badge>{Math.round(actionProgress.percent)}%</Badge>}
          </div>
          {actionProgress.percent > 0 && (
            <div className="studio-progress-track">
              <div
                className="studio-progress-fill"
                style={{
                  width: `${Math.max(
                    appConfig.studio.ui.minProgressFillPct,
                    Math.min(100, actionProgress.percent)
                  )}%`,
                }}
              />
            </div>
          )}
        </div>
      )}

      {/* Player Preview + Clip/Subtitle Inspector */}
      <div className="timeline-top-grid">
        <Card icon={Play} title={t("studio.timeline.previewTitle")}>
          <div className="timeline-player-stage">
            {activeVideoSrc ? (
              <video
                ref={setVideoRef}
                className="timeline-player-video"
                src={activeVideoSrc}
                preload="auto"
                playsInline
                style={{ opacity: previewState.transitionProgress }}
                onError={(e) =>
                  handleAssetMediaError(previewState.videoClip?.asset_id, e.currentTarget)
                }
              />
            ) : (
              <div className="timeline-player-empty">
                {timeline && (timeline.tracks ?? []).some((tr) => (tr.clips ?? []).length > 0)
                  ? t("studio.timeline.noClipAtPlayhead")
                  : t("studio.timeline.emptyTimeline")}
              </div>
            )}

            {/* Real-time synchronized audio elements */}
            {activeAudioTracks.map((tr) => {
              const activeCue = activeAudioCueByTrack.get(tr.id);
              const assetId = activeCue?.clip.asset_id;
              const src = assetId ? signedUrls[assetId] : undefined;
              return (
                <audio
                  key={tr.id}
                  ref={(el) => {
                    if (!el) {
                      audioRefs.current[tr.id]?.pause();
                    }
                    audioRefs.current[tr.id] = el;
                  }}
                  src={src}
                  preload="auto"
                  onError={(e) => handleAssetMediaError(assetId, e.currentTarget)}
                />
              );
            })}

            {previewState.activeSubtitles.length > 0 && (
              <div className="timeline-subtitle-overlay">
                {previewState.activeSubtitles.map((cue) => (
                  <div key={cue.id} className="timeline-subtitle-line">
                    {cue.speaker ? `${cue.speaker}: ` : ""}
                    {cue.text}
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="timeline-transport">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => seekTo(0)}
              title={t("studio.timeline.rewindStart")}
              aria-label={t("studio.timeline.rewindStart")}
            >
              <SkipBack size={14} />
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => setPlaying((p) => !p)}
              title={playing ? t("studio.timeline.pause") : t("studio.timeline.play")}
              aria-label={playing ? t("studio.timeline.pause") : t("studio.timeline.play")}
            >
              {playing ? <Pause size={14} /> : <Play size={14} />}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => seekTo(clampedPlayheadMs + appConfig.studio.timeline.nudgeStepMs)}
              title={t("studio.timeline.stepForward")}
              aria-label={t("studio.timeline.stepForward")}
            >
              <SkipForward size={14} />
            </Button>
            <input
              type="range"
              className="timeline-scrubber"
              min={0}
              max={durationMs}
              step={appConfig.studio.timeline.scrubStepMs}
              value={clampedPlayheadMs}
              onChange={(e) => seekTo(Number(e.target.value))}
              aria-label={t("studio.timeline.scrubLabel")}
            />
            <span className="timeline-timecode">
              {formatTimecodeMs(clampedPlayheadMs)} / {formatTimecodeMs(durationMs)}
            </span>
          </div>
        </Card>

        <Card
          icon={Scissors}
          title={
            selectedEntry
              ? t("studio.timeline.clipInspector", {
                  name: selectedEntry.clip.label || selectedEntry.clip.id,
                })
              : t("studio.timeline.subtitlesTitle")
          }
          actions={
            selectedEntry ? (
              <Button variant="ghost" size="sm" onClick={() => setSelectedClipId(null)}>
                {t("studio.timeline.viewSubtitles")}
              </Button>
            ) : undefined
          }
        >
          <div className="timeline-inspector-section">
            {selectedEntry ? (
              <ClipInspectorForm
                key={`${selectedEntry.clip.id}:${selectedEntry.clip.label ?? ""}:${selectedEntry.clip.speed}:${selectedEntry.clip.volume}:${Boolean(selectedEntry.clip.mute)}:${selectedEntry.clip.transition_in}:${selectedEntry.clip.transition_ms}:${selectedEntry.clip.ken_burns_preset ?? ""}`}
                clip={selectedEntry.clip}
                locked={selectedEntry.track.locked}
                busy={busy}
                canSplit={canSplitSelected}
                playheadMs={clampedPlayheadMs}
                loadingPeaks={Boolean(peaksLoadingClipIds[selectedEntry.clip.id])}
                onApplyOps={applyTimelineOps}
                onComputePeaks={(mediaPath, clipId) => computeClipPeaksThumbs(mediaPath, clipId)}
                onDeleted={() => setSelectedClipId(null)}
              />
            ) : (
              <>
                <div className="studio-field-row">
                  <input
                    className="input-control"
                    placeholder={t("studio.timeline.subtitleSpeakerPlaceholder")}
                    aria-label={t("studio.timeline.subtitleSpeakerPlaceholder")}
                    value={newSubSpeaker}
                    onChange={(e) => setNewSubSpeaker(e.target.value)}
                    disabled={busy}
                  />
                  <input
                    className="input-control"
                    placeholder={t("studio.timeline.subtitleTextPlaceholder")}
                    aria-label={t("studio.timeline.subtitleTextPlaceholder")}
                    value={newSubText}
                    onChange={(e) => setNewSubText(e.target.value)}
                    disabled={busy}
                  />
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void addSubtitleCue()}
                    disabled={busy || !newSubText.trim()}
                  >
                    <Plus size={14} />
                    {t("studio.timeline.addSubtitle")}
                  </Button>
                </div>

                <div className="timeline-sub-list">
                  {(timeline?.subtitles ?? []).length === 0 ? (
                    <p>{t("studio.timeline.noSubtitles")}</p>
                  ) : (
                    (timeline?.subtitles ?? []).map((cue) => {
                      const active =
                        clampedPlayheadMs >= cue.start_ms && clampedPlayheadMs <= cue.end_ms;
                      const isEditing = editingSub?.id === cue.id;
                      return (
                        <div
                          key={cue.id}
                          className={`timeline-sub-row ${active ? "timeline-sub-row--active" : ""}`}
                        >
                          {isEditing && editingSub ? (
                            <>
                              <div className="studio-field-row">
                                <input
                                  type="number"
                                  className="input-control"
                                  aria-label={t("studio.timeline.cueStartMs")}
                                  value={editingSub.start_ms}
                                  onChange={(e) =>
                                    setEditingSub({
                                      ...editingSub,
                                      start_ms: Number(e.target.value) || 0,
                                    })
                                  }
                                />
                                <input
                                  type="number"
                                  className="input-control"
                                  aria-label={t("studio.timeline.cueEndMs")}
                                  value={editingSub.end_ms}
                                  onChange={(e) =>
                                    setEditingSub({
                                      ...editingSub,
                                      end_ms: Number(e.target.value) || 0,
                                    })
                                  }
                                />
                              </div>
                              <input
                                className="input-control"
                                aria-label={t("studio.timeline.subtitleTextPlaceholder")}
                                value={editingSub.text}
                                onChange={(e) =>
                                  setEditingSub({ ...editingSub, text: e.target.value })
                                }
                              />
                              <div className="studio-toolbar">
                                <Button
                                  variant="primary"
                                  size="sm"
                                  disabled={busy}
                                  onClick={() => void saveSubtitleEdit()}
                                >
                                  {t("studio.common.save")}
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="sm"
                                  onClick={() => setEditingSub(null)}
                                >
                                  {t("studio.modal.cancel")}
                                </Button>
                              </div>
                            </>
                          ) : (
                            <div className="timeline-track-title">
                              <button
                                type="button"
                                className="timeline-mini-btn"
                                onClick={() => seekTo(cue.start_ms)}
                              >
                                {formatTimecodeMs(cue.start_ms)} - {formatTimecodeMs(cue.end_ms)}
                              </button>
                              <span>
                                {cue.speaker ? `${cue.speaker}: ` : ""}
                                {cue.text}
                              </span>
                              <div className="timeline-track-controls">
                                <button
                                  type="button"
                                  className="timeline-mini-btn"
                                  disabled={busy}
                                  onClick={() => setEditingSub(cue)}
                                >
                                  {t("studio.timeline.editCue")}
                                </button>
                                <button
                                  type="button"
                                  className="timeline-mini-btn"
                                  disabled={busy}
                                  aria-label={t("studio.timeline.deleteCue")}
                                  title={t("studio.timeline.deleteCue")}
                                  onClick={() =>
                                    void applyTimelineOps(
                                      [
                                        {
                                          op: "remove_subtitle" satisfies StudioTimelineOpKind,
                                          subtitle_id: cue.id,
                                        },
                                      ],
                                      t("studio.timeline.op.removeSubtitle")
                                    )
                                  }
                                >
                                  <Trash2 size={12} />
                                </button>
                              </div>
                            </div>
                          )}
                        </div>
                      );
                    })
                  )}
                </div>
              </>
            )}
          </div>
        </Card>
      </div>

      {/* Multi-Track Timeline Canvas */}
      <div className="timeline-tracks-card">
        <div className="timeline-tracks-scroller">
          {/* Ruler Row */}
          <div className="timeline-ruler-row">
            <div className="timeline-track-header">
              <div className="timeline-track-title">
                <span>
                  {timeline && timeline.width > 0 && timeline.height > 0
                    ? t("studio.timeline.specHeader", {
                        width: timeline.width,
                        height: timeline.height,
                        fps: timeline.fps,
                      })
                    : t("studio.timeline.tracksHeader")}
                </span>
              </div>
            </div>
            <div
              className="timeline-ruler-canvas"
              style={{ width: `${laneWidthPx}px` }}
              onClick={(e) => {
                const rect = e.currentTarget.getBoundingClientRect();
                const x = Math.max(0, e.clientX - rect.left);
                seekTo((x / pxPerSec) * 1000);
              }}
            >
              {rulerTicks.map((s) => (
                <div key={s} className="timeline-ruler-tick" style={{ left: `${s * pxPerSec}px` }}>
                  {t("studio.common.secondsShort", { sec: s })}
                </div>
              ))}
              <div
                className="timeline-playhead-line"
                style={{ left: `${(clampedPlayheadMs / 1000) * pxPerSec}px` }}
              />
            </div>
          </div>

          {/* Media Tracks */}
          {(timeline?.tracks ?? []).map((tr) => {
            const clipVariant =
              tr.kind === ("video" satisfies StudioTrackKind)
                ? "timeline-clip--video"
                : tr.kind === ("audio_bgm" satisfies StudioTrackKind)
                  ? "timeline-clip--bgm"
                  : "timeline-clip--audio";
            return (
              <div key={tr.id} className="timeline-lane-row">
                <div className="timeline-track-header">
                  <div className="timeline-track-title">
                    <span>{tr.name || tr.id}</span>
                    <Badge>{t(`studio.timeline.trackKinds.${tr.kind}` as never)}</Badge>
                  </div>
                  <div className="timeline-track-controls">
                    <button
                      type="button"
                      title={t("studio.timeline.muteLabel")}
                      aria-label={t("studio.timeline.muteLabel")}
                      className={`timeline-mini-btn ${tr.muted ? "timeline-mini-btn--active" : ""}`}
                      onClick={() =>
                        void applyTimelineOps(
                          [
                            {
                              op: "update_track" satisfies StudioTimelineOpKind,
                              track_id: tr.id,
                              muted: !tr.muted,
                            },
                          ],
                          t("studio.timeline.op.toggleMute")
                        )
                      }
                      disabled={busy}
                    >
                      {t("studio.timeline.muteShort")}
                    </button>
                    <button
                      type="button"
                      title={t("studio.timeline.soloLabel")}
                      aria-label={t("studio.timeline.soloLabel")}
                      className={`timeline-mini-btn ${tr.solo ? "timeline-mini-btn--active" : ""}`}
                      onClick={() =>
                        void applyTimelineOps(
                          [
                            {
                              op: "update_track" satisfies StudioTimelineOpKind,
                              track_id: tr.id,
                              solo: !tr.solo,
                            },
                          ],
                          t("studio.timeline.op.toggleSolo")
                        )
                      }
                      disabled={busy}
                    >
                      {t("studio.timeline.soloShort")}
                    </button>
                    <button
                      type="button"
                      title={t("studio.timeline.lockLabel")}
                      aria-label={t("studio.timeline.lockLabel")}
                      className={`timeline-mini-btn ${tr.locked ? "timeline-mini-btn--active" : ""}`}
                      onClick={() =>
                        void applyTimelineOps(
                          [
                            {
                              op: "update_track" satisfies StudioTimelineOpKind,
                              track_id: tr.id,
                              locked: !tr.locked,
                            },
                          ],
                          t("studio.timeline.op.toggleLock")
                        )
                      }
                      disabled={busy}
                    >
                      {t("studio.timeline.lockShort")}
                    </button>
                    <Volume2 size={12} />
                    <span>{Math.round(tr.volume * 100)}%</span>
                  </div>
                </div>

                <div className="timeline-lane-canvas" style={{ width: `${laneWidthPx}px` }}>
                  {(tr.clips ?? []).map((cl) => {
                    const leftPx = (cl.timeline_start_ms / 1000) * pxPerSec;
                    const widthPx = Math.max(
                      appConfig.studio.timeline.minClipWidthPx,
                      (clipSpanMs(cl) / 1000) * pxPerSec
                    );
                    const spriteUrl = cl.sprite_asset_id
                      ? signedUrls[cl.sprite_asset_id]
                      : undefined;
                    const isSel = selectedClipId === cl.id;
                    return (
                      <div
                        key={cl.id}
                        role="button"
                        tabIndex={0}
                        className={`timeline-clip ${clipVariant} ${isSel ? "timeline-clip--selected" : ""}`}
                        style={{ left: `${leftPx}px`, width: `${widthPx}px` }}
                        onClick={() => {
                          setSelectedClipId(cl.id);
                          seekTo(cl.timeline_start_ms);
                        }}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            setSelectedClipId(cl.id);
                          }
                        }}
                      >
                        {spriteUrl && (
                          <img
                            className="timeline-clip-sprite"
                            src={spriteUrl}
                            alt=""
                            onError={(e) =>
                              handleAssetMediaError(cl.sprite_asset_id, e.currentTarget)
                            }
                          />
                        )}
                        <div className="timeline-clip-header">
                          <span>{cl.label || cl.id}</span>
                          <span>
                            {t("studio.common.secondsShort", {
                              sec: (clipSpanMs(cl) / 1000).toFixed(1),
                            })}
                          </span>
                        </div>
                        {cl.peaks && cl.peaks.length > 0 && (
                          <svg
                            className="timeline-clip-waveform"
                            viewBox={`0 0 ${cl.peaks.length * appConfig.studio.timeline.waveformWidthStepPx} ${appConfig.studio.timeline.waveformHeightPx}`}
                            preserveAspectRatio="none"
                          >
                            {cl.peaks.map((pk, idx) => {
                              const h = Math.max(
                                appConfig.studio.timeline.waveformBarMinHeightPx,
                                Math.round(pk * appConfig.studio.timeline.waveformBarMaxHeightPx)
                              );
                              return (
                                <rect
                                  key={idx}
                                  className="timeline-waveform-bar"
                                  x={idx * appConfig.studio.timeline.waveformWidthStepPx}
                                  y={appConfig.studio.timeline.waveformHeightPx - h}
                                  width={appConfig.studio.timeline.waveformBarWidthPx}
                                  height={h}
                                />
                              );
                            })}
                          </svg>
                        )}
                      </div>
                    );
                  })}
                  <div
                    className="timeline-playhead-line"
                    style={{ left: `${(clampedPlayheadMs / 1000) * pxPerSec}px` }}
                  />
                </div>
              </div>
            );
          })}

          {/* Dedicated Subtitles Track Lane */}
          <div className="timeline-lane-row">
            <div className="timeline-track-header">
              <div className="timeline-track-title">
                <span>{t("studio.timeline.subtitlesLane")}</span>
                <Badge>{(timeline?.subtitles ?? []).length}</Badge>
              </div>
            </div>
            <div className="timeline-lane-canvas" style={{ width: `${laneWidthPx}px` }}>
              {(timeline?.subtitles ?? []).map((cue) => {
                const leftPx = (cue.start_ms / 1000) * pxPerSec;
                const widthPx = Math.max(
                  appConfig.studio.timeline.minSubClipWidthPx,
                  ((cue.end_ms - cue.start_ms) / 1000) * pxPerSec
                );
                return (
                  <div
                    key={cue.id}
                    role="button"
                    tabIndex={0}
                    className="timeline-clip timeline-clip--subtitle"
                    style={{ left: `${leftPx}px`, width: `${widthPx}px` }}
                    onClick={() => {
                      setSelectedClipId(null);
                      setEditingSub(cue);
                      seekTo(cue.start_ms);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        setEditingSub(cue);
                      }
                    }}
                  >
                    <div className="timeline-clip-header">
                      <span>{cue.text}</span>
                    </div>
                  </div>
                );
              })}
              <div
                className="timeline-playhead-line"
                style={{ left: `${(clampedPlayheadMs / 1000) * pxPerSec}px` }}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Rendered Output & NLE Export Artifacts */}
      <Card
        icon={Film}
        title={t("studio.timeline.exportsTitle")}
        actions={
          <Button
            variant="ghost"
            size="sm"
            onClick={() => void openExportFolder(finalVideoPath || lastNLEExport?.export_path)}
          >
            <FolderOpen size={14} />
            {t("studio.export.openFolder")}
          </Button>
        }
      >
        <div className="timeline-export-summary">
          <div className="timeline-export-artifact">
            <div className="timeline-track-title">
              <span>{t("studio.timeline.finalMP4Card")}</span>
              {lastRenderResult?.duration_sec ? (
                <Badge>
                  {t("studio.common.secondsShort", {
                    sec: lastRenderResult.duration_sec.toFixed(1),
                  })}
                </Badge>
              ) : null}
            </div>
            <div className="studio-media-frame studio-media-frame--export">
              <SignedStudioVideo
                assetId={finalVideoAssetId}
                placeholder={t("studio.export.noOutput")}
                large
              />
            </div>
            {finalVideoPath && (
              <div className="studio-toolbar">
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() =>
                    void saveMediaToDevice(
                      finalVideoPath,
                      mediaBasename(finalVideoPath),
                      undefined,
                      finalVideoAssetId
                    )
                  }
                >
                  <Download size={14} />
                  {t("studio.export.download")}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    askAgentAboutMediaAsset({
                      assetId: finalVideoAssetId,
                      filePath: finalVideoPath,
                      mimeType: "video/mp4",
                    })
                  }
                >
                  <Bot size={14} />
                  {t("studio.common.askAgent")}
                </Button>
              </div>
            )}
          </div>

          {lastNLEExport &&
            (() => {
              const nleBadgeKey = `studio.timeline.nleBadge.${lastNLEExport.format}`;
              return (
                <div className="timeline-export-artifact">
                  <div className="timeline-track-title">
                    <span>{t("studio.timeline.nlePackageReady")}</span>
                    <Badge>
                      {isMessageKey(nleBadgeKey)
                        ? t(nleBadgeKey)
                        : lastNLEExport.format.toUpperCase()}
                    </Badge>
                  </div>
                  <p>{mediaBasename(lastNLEExport.export_path)}</p>
                  <div className="studio-toolbar">
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() =>
                        void saveMediaToDevice(
                          lastNLEExport.export_path,
                          mediaBasename(lastNLEExport.export_path),
                          undefined,
                          lastNLEExport.asset_id
                        )
                      }
                    >
                      <Download size={14} />
                      {t("studio.timeline.downloadNLE")}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => void openExportFolder(lastNLEExport.export_path)}
                    >
                      <FolderOpen size={14} />
                      {t("studio.export.openFolder")}
                    </Button>
                  </div>
                </div>
              );
            })()}
        </div>
      </Card>

      {showRenderDialog && (
        <RenderTimelineDialog
          defaultFilename={`${detail.project.slug}_${detail.episode.code}_final.mp4`}
          onClose={() => setShowRenderDialog(false)}
        />
      )}
      {showNLEDialog && (
        <ExportNLEDraftDialog
          defaultProjectName={`${detail.project.title} ${detail.episode.code}`}
          onClose={() => setShowNLEDialog(false)}
        />
      )}
    </div>
  );
};
