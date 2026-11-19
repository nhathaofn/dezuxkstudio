import React from "react";
import { Plus, Trash2, Volume2 } from "lucide-react";
import { Badge } from "../../../components/ui/Badge";
import { Button } from "../../../components/ui/Button";
import { appConfig } from "../../../config/appConfig";
import {
  StudioTrackKindValues,
  type StudioEditTimeline,
  type StudioSubtitleCue,
  type StudioTimelineOp,
  type StudioTimelineOpKind,
  type StudioTrackKind,
} from "../../../contracts/generated";
import { useT } from "../../../lib/i18n";
import { WaveformCanvas } from "./WaveformCanvas";
import { clipSpanMs, formatTimecodeMs } from "./timelineHelpers";

export interface TimelineTrackLanesProps {
  timeline: StudioEditTimeline | null;
  busy: boolean;
  pxPerSec: number;
  laneWidthPx: number;
  rulerTicks: number[];
  clampedPlayheadMs: number;
  selectedClipId: string | null;
  signedUrls: Record<string, string>;
  newTrackKind: StudioTrackKind;
  newTrackName: string;
  markerLabel: string;
  registerPlayheadLine: (key: string) => (el: HTMLDivElement | null) => void;
  handleAssetMediaError: (assetId: string | undefined, el: HTMLImageElement) => void;
  onChangeNewTrackKind: (kind: StudioTrackKind) => void;
  onChangeNewTrackName: (name: string) => void;
  onAddTrack: () => Promise<void>;
  onChangeMarkerLabel: (label: string) => void;
  onAddMarker: () => Promise<void>;
  onSeekTo: (ms: number) => void;
  onSelectClip: (clipId: string | null) => void;
  onEditSub: (cue: StudioSubtitleCue) => void;
  onApplyOps: (ops: StudioTimelineOp[], summary: string) => Promise<boolean>;
}

export const TimelineTrackLanes: React.FC<TimelineTrackLanesProps> = ({
  timeline,
  busy,
  pxPerSec,
  laneWidthPx,
  rulerTicks,
  clampedPlayheadMs,
  selectedClipId,
  signedUrls,
  newTrackKind,
  newTrackName,
  markerLabel,
  registerPlayheadLine,
  handleAssetMediaError,
  onChangeNewTrackKind,
  onChangeNewTrackName,
  onAddTrack,
  onChangeMarkerLabel,
  onAddMarker,
  onSeekTo,
  onSelectClip,
  onEditSub,
  onApplyOps,
}) => {
  const t = useT();
  return (
    <div className="timeline-tracks-card">
      <div className="timeline-toolbar-card">
        <div className="studio-field-row">
          <select
            className="input-control"
            value={newTrackKind}
            onChange={(e) => onChangeNewTrackKind(e.target.value as StudioTrackKind)}
            disabled={busy}
            aria-label={t("studio.timeline.addTrack")}
          >
            {StudioTrackKindValues.map((k) => (
              <option key={k} value={k}>
                {t(`studio.timeline.trackKinds.${k}` as never)}
              </option>
            ))}
          </select>
          <input
            className="input-control"
            placeholder={t("studio.timeline.trackNamePlaceholder")}
            value={newTrackName}
            onChange={(e) => onChangeNewTrackName(e.target.value)}
            disabled={busy}
          />
          <Button variant="secondary" size="sm" disabled={busy} onClick={() => void onAddTrack()}>
            <Plus size={13} />
            {t("studio.timeline.addTrack")}
          </Button>
        </div>

        <div className="studio-field-row">
          <input
            className="input-control"
            placeholder={t("studio.timeline.markerPlaceholder")}
            value={markerLabel}
            onChange={(e) => onChangeMarkerLabel(e.target.value)}
            disabled={busy || !timeline}
          />
          <Button
            variant="secondary"
            size="sm"
            disabled={busy || !timeline}
            onClick={() => void onAddMarker()}
          >
            <Plus size={13} />
            {t("studio.timeline.addMarker")}
          </Button>
        </div>
      </div>

      {(timeline?.markers ?? []).length > 0 && (
        <div className="studio-toolbar">
          {(timeline?.markers ?? []).map((mk) => (
            <div key={mk.id} className="timeline-marker-chip">
              <button
                type="button"
                className="timeline-mini-btn"
                onClick={() => onSeekTo(mk.time_ms)}
              >
                {mk.label} ({formatTimecodeMs(mk.time_ms)})
              </button>
              <button
                type="button"
                className="timeline-mini-btn"
                disabled={busy}
                aria-label={t("studio.timeline.removeMarker")}
                title={t("studio.timeline.removeMarker")}
                onClick={() =>
                  void onApplyOps(
                    [{ op: "remove_marker" satisfies StudioTimelineOpKind, marker_id: mk.id }],
                    t("studio.timeline.op.removeMarker")
                  )
                }
              >
                ×
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="timeline-tracks-scroller">
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
              onSeekTo((x / pxPerSec) * 1000);
            }}
          >
            {rulerTicks.map((s) => (
              <div key={s} className="timeline-ruler-tick" style={{ left: `${s * pxPerSec}px` }}>
                {t("studio.common.secondsShort", { sec: s })}
              </div>
            ))}
            {(timeline?.markers ?? []).map((mk) => (
              <button
                key={mk.id}
                type="button"
                className="timeline-marker-pin"
                style={{ left: `${(mk.time_ms / 1000) * pxPerSec}px` }}
                title={`${mk.label} (${formatTimecodeMs(mk.time_ms)})`}
                onClick={(e) => {
                  e.stopPropagation();
                  onSeekTo(mk.time_ms);
                }}
              >
                {mk.label}
              </button>
            ))}
            <div
              ref={registerPlayheadLine("ruler")}
              className="timeline-playhead-line"
              style={{
                left: 0,
                transform: `translate3d(${(clampedPlayheadMs / 1000) * pxPerSec}px, 0, 0)`,
              }}
            />
          </div>
        </div>

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
                      void onApplyOps(
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
                      void onApplyOps(
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
                      void onApplyOps(
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
                  <button
                    type="button"
                    className="timeline-mini-btn"
                    disabled={busy}
                    aria-label={t("studio.timeline.removeTrack")}
                    title={t("studio.timeline.removeTrack")}
                    onClick={() =>
                      void onApplyOps(
                        [{ op: "remove_track" satisfies StudioTimelineOpKind, track_id: tr.id }],
                        t("studio.timeline.op.removeTrack", { name: tr.name || tr.id })
                      )
                    }
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              </div>

              <div className="timeline-lane-canvas" style={{ width: `${laneWidthPx}px` }}>
                {(tr.clips ?? []).map((cl) => {
                  const leftPx = (cl.timeline_start_ms / 1000) * pxPerSec;
                  const widthPx = Math.max(
                    appConfig.studio.timeline.minClipWidthPx,
                    (clipSpanMs(cl) / 1000) * pxPerSec
                  );
                  const spriteUrl = cl.sprite_asset_id ? signedUrls[cl.sprite_asset_id] : undefined;
                  const isSel = selectedClipId === cl.id;
                  return (
                    <div
                      key={cl.id}
                      role="button"
                      tabIndex={0}
                      className={`timeline-clip ${clipVariant} ${isSel ? "timeline-clip--selected" : ""}`}
                      style={{ left: `${leftPx}px`, width: `${widthPx}px` }}
                      onClick={() => {
                        onSelectClip(cl.id);
                        onSeekTo(cl.timeline_start_ms);
                      }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          onSelectClip(cl.id);
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
                      {cl.peaks && cl.peaks.length > 0 && <WaveformCanvas peaks={cl.peaks} />}
                    </div>
                  );
                })}
                <div
                  ref={registerPlayheadLine(`track:${tr.id}`)}
                  className="timeline-playhead-line"
                  style={{
                    left: 0,
                    transform: `translate3d(${(clampedPlayheadMs / 1000) * pxPerSec}px, 0, 0)`,
                  }}
                />
              </div>
            </div>
          );
        })}

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
                    onSelectClip(null);
                    onEditSub(cue);
                    onSeekTo(cue.start_ms);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onEditSub(cue);
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
              ref={registerPlayheadLine("subtitles")}
              className="timeline-playhead-line"
              style={{
                left: 0,
                transform: `translate3d(${(clampedPlayheadMs / 1000) * pxPerSec}px, 0, 0)`,
              }}
            />
          </div>
        </div>
      </div>
    </div>
  );
};
