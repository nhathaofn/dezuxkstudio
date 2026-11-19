import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Clapperboard,
  FileArchive,
  Loader2,
  Plus,
  Redo2,
  RefreshCw,
  Sparkles,
  Trash2,
  Undo2,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Badge } from "../../../components/ui/Badge";
import { Button } from "../../../components/ui/Button";
import { appConfig } from "../../../config/appConfig";
import type {
  StudioEditTimeline,
  StudioEpisodeDetailResult,
  StudioSubtitleCue,
  StudioTimelineOpKind,
  StudioTrackKind,
} from "../../../contracts/generated";
import { useSignedMediaURLs } from "../../../lib/hooks/useSignedMediaURL";
import { translateServerMessage, useT } from "../../../lib/i18n";
import { mediaBasename } from "../../../lib/rtClient";
import { useStudioStore } from "../useStudioStore";
import {
  AddClipDialog,
  ExportNLEDraftDialog,
  RenderTimelineDialog,
  ReplaceTimelineConfirmDialog,
} from "./TimelineDialogs";
import { TimelineExportBar } from "./TimelineExportBar";
import { ClipInspectorForm, TimelineInspectorCard } from "./TimelineInspector";
import { TimelineTrackLanes } from "./TimelineTrackLanes";
import { TimelineTransport } from "./TimelineTransport";
import {
  clipEndMs,
  collectTimelineAssetIds,
  computeTimelineDurationMs,
  findClipInTimeline,
  formatTimecodeMs,
  isAudioTrackKind,
  resolvePreviewStateAtTime,
} from "./timelineHelpers";
import "./TimelineWorkspace.css";

export { ClipInspectorForm, ExportNLEDraftDialog, RenderTimelineDialog };

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
  const [showAddClipDialog, setShowAddClipDialog] = useState(false);
  const [showReplaceConfirm, setShowReplaceConfirm] = useState(false);
  const [showFormatForm, setShowFormatForm] = useState(false);
  const [fmtFps, setFmtFps] = useState(24);
  const [fmtWidth, setFmtWidth] = useState(1280);
  const [fmtHeight, setFmtHeight] = useState(720);
  const [newTrackKind, setNewTrackKind] = useState<StudioTrackKind>("video");
  const [newTrackName, setNewTrackName] = useState("");
  const [markerLabel, setMarkerLabel] = useState("");
  const [addClipTrackId, setAddClipTrackId] = useState("");
  const [addClipShotKey, setAddClipShotKey] = useState("");
  const [addClipCustomPath, setAddClipCustomPath] = useState("");
  const [addClipLabel, setAddClipLabel] = useState("");
  const [addClipDurationMs, setAddClipDurationMs] = useState(4000);

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
  const playheadLinesRef = useRef<Map<string, HTMLDivElement>>(new Map());
  const scrubberRef = useRef<HTMLInputElement | null>(null);
  const timecodeCurRef = useRef<HTMLSpanElement | null>(null);

  const registerPlayheadLine = useCallback(
    (key: string) => (el: HTMLDivElement | null) => {
      if (el) {
        playheadLinesRef.current.set(key, el);
      } else {
        playheadLinesRef.current.delete(key);
      }
    },
    []
  );

  const applyPlayheadDOM = useCallback(
    (ms: number) => {
      const clamped = Math.max(0, Math.min(durationMs, ms));
      const xPx = (clamped / 1000) * pxPerSec;
      const transform = `translate3d(${xPx}px, 0, 0)`;
      for (const el of playheadLinesRef.current.values()) {
        el.style.transform = transform;
      }
      if (timecodeCurRef.current) {
        timecodeCurRef.current.textContent = formatTimecodeMs(clamped);
      }
      if (scrubberRef.current) {
        scrubberRef.current.value = String(Math.round(clamped));
      }
    },
    [durationMs, pxPerSec]
  );

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
    const stateSignature = (ms: number) => {
      const st = resolvePreviewStateAtTime(timeline, ms);
      const vId = st.videoClip?.id ?? "";
      const aKey = st.audioCues.map((c) => `${c.trackId}:${c.clip.id}`).join(",");
      const sKey = st.activeSubtitles.map((c) => c.id).join(",");
      return `${vId}|${aKey}|${sKey}`;
    };
    let lastSig = stateSignature(playheadRef.current);

    const tick = (now: number) => {
      const dt = Math.max(0, now - lastTs);
      lastTs = now;
      const nextMs = Math.min(durationMs, playheadRef.current + dt);
      playheadRef.current = nextMs;
      applyPlayheadDOM(nextMs);
      syncMediaElements(nextMs, true);
      const nextSig = stateSignature(nextMs);
      if (nextSig !== lastSig) {
        lastSig = nextSig;
        setPlayheadMs(Math.round(nextMs));
      }
      if (nextMs >= durationMs) {
        setPlayheadMs(Math.round(nextMs));
        setPlaying(false);
        return;
      }
      rafId = requestAnimationFrame(tick);
    };

    rafId = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(rafId);
      setPlayheadMs(Math.round(playheadRef.current));
    };
  }, [playing, durationMs, timeline, applyPlayheadDOM, syncMediaElements]);

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
    applyPlayheadDOM(clamped);
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

  const openFormatPanel = () => {
    if (timeline) {
      setFmtFps(timeline.fps || 24);
      setFmtWidth(timeline.width || 1280);
      setFmtHeight(timeline.height || 720);
    }
    setShowFormatForm((v) => !v);
  };

  const handleAddTrack = async () => {
    const name = newTrackName.trim() || `${newTrackKind}-${(timeline?.tracks?.length ?? 0) + 1}`;
    const ok = await applyTimelineOps(
      [
        {
          op: "add_track" satisfies StudioTimelineOpKind,
          track_kind: newTrackKind,
          track_name: name,
        },
      ],
      t("studio.timeline.op.addTrack", { name })
    );
    if (ok) setNewTrackName("");
  };

  const handleAddMarker = async () => {
    const label = markerLabel.trim() || formatTimecodeMs(playheadRef.current);
    const id =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? `mk-${crypto.randomUUID()}`
        : `mk-${Date.now()}`;
    const ok = await applyTimelineOps(
      [
        {
          op: "add_marker" satisfies StudioTimelineOpKind,
          marker: {
            id,
            time_ms: Math.max(0, Math.round(playheadRef.current)),
            label,
          },
        },
      ],
      t("studio.timeline.op.addMarker", { label })
    );
    if (ok) setMarkerLabel("");
  };

  const shotMediaOptions = useMemo(() => {
    const items: Array<{
      key: string;
      label: string;
      path: string;
      assetId?: string;
      durationMs: number;
    }> = [];
    for (const sh of detail.shots) {
      if (sh.video_path) {
        items.push({
          key: `v:${sh.id}`,
          label: `#${sh.seq} ${sh.title} (video)`,
          path: sh.video_path,
          assetId: sh.video_asset_id || undefined,
          durationMs: Math.max(1000, Math.round((sh.duration_sec || 4) * 1000)),
        });
      }
      if (sh.audio_path) {
        items.push({
          key: `a:${sh.id}`,
          label: `#${sh.seq} ${sh.title} (audio)`,
          path: sh.audio_path,
          assetId: sh.audio_asset_id || undefined,
          durationMs: Math.max(1000, Math.round((sh.duration_sec || 4) * 1000)),
        });
      }
    }
    return items;
  }, [detail.shots]);

  const handleAddClipSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const targetTrack = addClipTrackId || timeline?.tracks?.[0]?.id || "";
    const picked = shotMediaOptions.find((o) => o.key === addClipShotKey);
    const filePath = (addClipCustomPath.trim() || picked?.path || "").trim();
    if (!targetTrack || !filePath) return;
    const dur = Math.max(100, Math.round(addClipDurationMs || picked?.durationMs || 4000));
    const clipId =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? `clip-${crypto.randomUUID()}`
        : `clip-${Date.now()}`;
    const label = addClipLabel.trim() || picked?.label || mediaBasename(filePath);
    const ok = await applyTimelineOps(
      [
        {
          op: "add_clip" satisfies StudioTimelineOpKind,
          target_track_id: targetTrack,
          clip: {
            id: clipId,
            label,
            file_path: filePath,
            asset_id: picked && !addClipCustomPath.trim() ? picked.assetId : undefined,
            source_in_ms: 0,
            source_out_ms: dur,
            timeline_start_ms: Math.max(0, Math.round(playheadRef.current)),
            speed: 1,
            volume: 1,
            mute: false,
            transition_in: "none",
            transition_ms: 0,
            ken_burns_preset: "none",
            peaks: [],
          },
        },
      ],
      t("studio.timeline.op.addClip", { name: label })
    );
    if (ok) setShowAddClipDialog(false);
  };

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
            onClick={() => {
              setAddClipTrackId(timeline?.tracks?.[0]?.id ?? "");
              setAddClipShotKey(shotMediaOptions[0]?.key ?? "");
              setAddClipDurationMs(shotMediaOptions[0]?.durationMs ?? 4000);
              setAddClipCustomPath("");
              setAddClipLabel("");
              setShowAddClipDialog(true);
            }}
            disabled={busy || !timeline || (timeline.tracks ?? []).length === 0}
          >
            <Plus size={14} />
            {t("studio.timeline.addClipBtn")}
          </Button>
          <Button variant="ghost" size="sm" onClick={openFormatPanel} disabled={busy || !timeline}>
            {t("studio.timeline.formatBtn")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setShowReplaceConfirm(true)}
            disabled={busy || !timeline}
          >
            <Trash2 size={14} />
            {t("studio.timeline.replaceBtn")}
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

      {showFormatForm && timeline && (
        <div className="timeline-toolbar-card">
          <div className="studio-field-row">
            <label className="studio-field-inline">
              <span>{t("studio.timeline.formatFps")}</span>
              <input
                type="number"
                min={1}
                max={120}
                className="input-control"
                value={fmtFps}
                onChange={(e) => setFmtFps(Math.max(1, Number(e.target.value) || 24))}
              />
            </label>
            <label className="studio-field-inline">
              <span>{t("studio.timeline.formatWidth")}</span>
              <input
                type="number"
                min={128}
                step={2}
                className="input-control"
                value={fmtWidth}
                onChange={(e) => setFmtWidth(Math.max(128, Number(e.target.value) || 1280))}
              />
            </label>
            <label className="studio-field-inline">
              <span>{t("studio.timeline.formatHeight")}</span>
              <input
                type="number"
                min={128}
                step={2}
                className="input-control"
                value={fmtHeight}
                onChange={(e) => setFmtHeight(Math.max(128, Number(e.target.value) || 720))}
              />
            </label>
            <Button
              variant="primary"
              size="sm"
              disabled={busy}
              onClick={() => {
                void applyTimelineOps(
                  [
                    {
                      op: "set_format" satisfies StudioTimelineOpKind,
                      fps: fmtFps,
                      width: fmtWidth,
                      height: fmtHeight,
                    },
                  ],
                  t("studio.timeline.op.setFormat")
                );
                setShowFormatForm(false);
              }}
            >
              {t("studio.timeline.applyFormat")}
            </Button>
          </div>
        </div>
      )}

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

      <div className="timeline-top-grid">
        <TimelineTransport
          timeline={timeline}
          activeVideoSrc={activeVideoSrc}
          videoClipAssetId={previewState.videoClip?.asset_id}
          transitionProgress={previewState.transitionProgress}
          activeSubtitles={previewState.activeSubtitles}
          activeAudioTracks={activeAudioTracks}
          activeAudioCueByTrack={activeAudioCueByTrack}
          signedUrls={signedUrls}
          audioRefs={audioRefs}
          scrubberRef={scrubberRef}
          timecodeCurRef={timecodeCurRef}
          setVideoRef={setVideoRef}
          handleAssetMediaError={handleAssetMediaError}
          playing={playing}
          onTogglePlaying={() => setPlaying((p) => !p)}
          durationMs={durationMs}
          clampedPlayheadMs={clampedPlayheadMs}
          seekTo={seekTo}
        />

        <TimelineInspectorCard
          timeline={timeline}
          selectedEntry={selectedEntry}
          busy={busy}
          canSplitSelected={canSplitSelected}
          clampedPlayheadMs={clampedPlayheadMs}
          peaksLoadingClipIds={peaksLoadingClipIds}
          newSubSpeaker={newSubSpeaker}
          newSubText={newSubText}
          editingSub={editingSub}
          onChangeNewSubSpeaker={setNewSubSpeaker}
          onChangeNewSubText={setNewSubText}
          onSetEditingSub={setEditingSub}
          onAddSubtitleCue={addSubtitleCue}
          onSaveSubtitleEdit={saveSubtitleEdit}
          onSelectClip={setSelectedClipId}
          onSeekTo={seekTo}
          onApplyOps={applyTimelineOps}
          onComputePeaks={computeClipPeaksThumbs}
        />
      </div>

      <TimelineTrackLanes
        timeline={timeline}
        busy={busy}
        pxPerSec={pxPerSec}
        laneWidthPx={laneWidthPx}
        rulerTicks={rulerTicks}
        clampedPlayheadMs={clampedPlayheadMs}
        selectedClipId={selectedClipId}
        signedUrls={signedUrls}
        newTrackKind={newTrackKind}
        newTrackName={newTrackName}
        markerLabel={markerLabel}
        registerPlayheadLine={registerPlayheadLine}
        handleAssetMediaError={handleAssetMediaError}
        onChangeNewTrackKind={setNewTrackKind}
        onChangeNewTrackName={setNewTrackName}
        onAddTrack={handleAddTrack}
        onChangeMarkerLabel={setMarkerLabel}
        onAddMarker={handleAddMarker}
        onSeekTo={seekTo}
        onSelectClip={setSelectedClipId}
        onEditSub={setEditingSub}
        onApplyOps={applyTimelineOps}
      />

      <TimelineExportBar
        finalVideoPath={finalVideoPath}
        finalVideoAssetId={finalVideoAssetId}
        lastRenderResult={lastRenderResult}
        lastNLEExport={lastNLEExport}
        openExportFolder={openExportFolder}
      />

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
      {showAddClipDialog && timeline && (
        <AddClipDialog
          timeline={timeline}
          shotMediaOptions={shotMediaOptions}
          busy={busy}
          trackId={addClipTrackId}
          shotKey={addClipShotKey}
          customPath={addClipCustomPath}
          clipLabel={addClipLabel}
          durationMs={addClipDurationMs}
          onChangeTrackId={setAddClipTrackId}
          onChangeShotKey={setAddClipShotKey}
          onChangeCustomPath={setAddClipCustomPath}
          onChangeLabel={setAddClipLabel}
          onChangeDurationMs={setAddClipDurationMs}
          onSubmit={(e) => void handleAddClipSubmit(e)}
          onClose={() => setShowAddClipDialog(false)}
        />
      )}
      {showReplaceConfirm && timeline && (
        <ReplaceTimelineConfirmDialog
          timeline={timeline}
          busy={busy}
          onApplyOps={applyTimelineOps}
          onClose={() => setShowReplaceConfirm(false)}
        />
      )}
    </div>
  );
};
