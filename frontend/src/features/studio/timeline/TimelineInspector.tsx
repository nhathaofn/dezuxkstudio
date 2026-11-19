import React, { useState } from "react";
import { Loader2, Plus, Scissors, Trash2, Waves } from "lucide-react";
import { Button } from "../../../components/ui/Button";
import { Card } from "../../../components/ui/Card";
import { Switch } from "../../../components/ui/Switch";
import { appConfig } from "../../../config/appConfig";
import {
  NodeTypes,
  StudioKenBurnsPresetValues,
  StudioTransitionTypeValues,
  type StudioEditTimeline,
  type StudioKenBurnsPreset,
  type StudioSubtitleCue,
  type StudioTimelineClip,
  type StudioTimelineOp,
  type StudioTimelineOpKind,
  type StudioTimelineTrack,
  type StudioTransitionType,
} from "../../../contracts/generated";
import { useT } from "../../../lib/i18n";
import { findParam, useNodeSpec } from "../../workflow/useNodeSpecs";
import { buildClipTrimOp, formatTimecodeMs } from "./timelineHelpers";

interface ClipInspectorProps {
  clip: StudioTimelineClip;
  trackId?: string;
  tracks?: StudioTimelineTrack[];
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
  trackId,
  tracks,
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
  const [moveTrackId, setMoveTrackId] = useState(trackId ?? "");
  const [moveStartMs, setMoveStartMs] = useState(clip.timeline_start_ms);
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

      <div className="studio-field-row">
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-move-track">
            {t("studio.timeline.moveTargetTrack")}
          </label>
          <select
            id="tl-clip-move-track"
            className="input-control"
            value={moveTrackId}
            onChange={(e) => setMoveTrackId(e.target.value)}
            disabled={busy || locked}
          >
            {(tracks ?? []).map((tr) => (
              <option key={tr.id} value={tr.id}>
                {tr.name || tr.id}
              </option>
            ))}
          </select>
        </div>
        <div className="form-group">
          <label className="field-label" htmlFor="tl-clip-move-start">
            {t("studio.timeline.moveStartMs")}
          </label>
          <input
            id="tl-clip-move-start"
            type="number"
            min={0}
            step={100}
            className="input-control input-control--mono"
            value={moveStartMs}
            onChange={(e) => setMoveStartMs(Math.max(0, Number(e.target.value) || 0))}
            disabled={busy || locked}
          />
        </div>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || locked}
          onClick={() =>
            void onApplyOps(
              [
                {
                  op: "move_clip" satisfies StudioTimelineOpKind,
                  clip_id: clip.id,
                  target_track_id: moveTrackId || trackId,
                  timeline_start_ms: Math.max(0, Math.round(moveStartMs)),
                },
              ],
              t("studio.timeline.op.moveClip", { name: clip.label || clip.id })
            )
          }
        >
          {t("studio.timeline.moveClip")}
        </Button>
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

export interface TimelineInspectorCardProps {
  timeline: StudioEditTimeline | null;
  selectedEntry: { track: StudioTimelineTrack; clip: StudioTimelineClip } | null;
  busy: boolean;
  canSplitSelected: boolean;
  clampedPlayheadMs: number;
  peaksLoadingClipIds: Record<string, boolean>;
  newSubSpeaker: string;
  newSubText: string;
  editingSub: StudioSubtitleCue | null;
  onChangeNewSubSpeaker: (val: string) => void;
  onChangeNewSubText: (val: string) => void;
  onSetEditingSub: (cue: StudioSubtitleCue | null) => void;
  onAddSubtitleCue: () => Promise<void>;
  onSaveSubtitleEdit: () => Promise<void>;
  onSelectClip: (clipId: string | null) => void;
  onSeekTo: (ms: number) => void;
  onApplyOps: (ops: StudioTimelineOp[], summary?: string) => Promise<boolean>;
  onComputePeaks: (mediaPath: string, clipId?: string) => Promise<unknown>;
}

export const TimelineInspectorCard: React.FC<TimelineInspectorCardProps> = ({
  timeline,
  selectedEntry,
  busy,
  canSplitSelected,
  clampedPlayheadMs,
  peaksLoadingClipIds,
  newSubSpeaker,
  newSubText,
  editingSub,
  onChangeNewSubSpeaker,
  onChangeNewSubText,
  onSetEditingSub,
  onAddSubtitleCue,
  onSaveSubtitleEdit,
  onSelectClip,
  onSeekTo,
  onApplyOps,
  onComputePeaks,
}) => {
  const t = useT();
  return (
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
          <Button variant="ghost" size="sm" onClick={() => onSelectClip(null)}>
            {t("studio.timeline.viewSubtitles")}
          </Button>
        ) : undefined
      }
    >
      <div className="timeline-inspector-section">
        {selectedEntry ? (
          <ClipInspectorForm
            key={`${selectedEntry.clip.id}:${selectedEntry.track.id}:${selectedEntry.clip.timeline_start_ms}:${selectedEntry.clip.label ?? ""}:${selectedEntry.clip.speed}:${selectedEntry.clip.volume}:${Boolean(selectedEntry.clip.mute)}:${selectedEntry.clip.transition_in}:${selectedEntry.clip.transition_ms}:${selectedEntry.clip.ken_burns_preset ?? ""}`}
            clip={selectedEntry.clip}
            trackId={selectedEntry.track.id}
            tracks={timeline?.tracks ?? []}
            locked={selectedEntry.track.locked}
            busy={busy}
            canSplit={canSplitSelected}
            playheadMs={clampedPlayheadMs}
            loadingPeaks={Boolean(peaksLoadingClipIds[selectedEntry.clip.id])}
            onApplyOps={onApplyOps}
            onComputePeaks={(mediaPath, clipId) => onComputePeaks(mediaPath, clipId)}
            onDeleted={() => onSelectClip(null)}
          />
        ) : (
          <>
            <div className="studio-field-row">
              <input
                className="input-control"
                placeholder={t("studio.timeline.subtitleSpeakerPlaceholder")}
                aria-label={t("studio.timeline.subtitleSpeakerPlaceholder")}
                value={newSubSpeaker}
                onChange={(e) => onChangeNewSubSpeaker(e.target.value)}
                disabled={busy}
              />
              <input
                className="input-control"
                placeholder={t("studio.timeline.subtitleTextPlaceholder")}
                aria-label={t("studio.timeline.subtitleTextPlaceholder")}
                value={newSubText}
                onChange={(e) => onChangeNewSubText(e.target.value)}
                disabled={busy}
              />
              <Button
                variant="secondary"
                size="sm"
                onClick={() => void onAddSubtitleCue()}
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
                                onSetEditingSub({
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
                                onSetEditingSub({
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
                              onSetEditingSub({ ...editingSub, text: e.target.value })
                            }
                          />
                          <div className="studio-toolbar">
                            <Button
                              variant="primary"
                              size="sm"
                              disabled={busy}
                              onClick={() => void onSaveSubtitleEdit()}
                            >
                              {t("studio.common.save")}
                            </Button>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => onSetEditingSub(null)}
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
                            onClick={() => onSeekTo(cue.start_ms)}
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
                              onClick={() => onSetEditingSub(cue)}
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
                                void onApplyOps(
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
  );
};
