import React from "react";
import { Pause, Play, SkipBack, SkipForward } from "lucide-react";
import { Button } from "../../../components/ui/Button";
import { Card } from "../../../components/ui/Card";
import { appConfig } from "../../../config/appConfig";
import type {
  StudioEditTimeline,
  StudioSubtitleCue,
  StudioTimelineClip,
  StudioTimelineTrack,
} from "../../../contracts/generated";
import { useT } from "../../../lib/i18n";
import { formatTimecodeMs } from "./timelineHelpers";

export interface TimelineTransportProps {
  timeline: StudioEditTimeline | null;
  activeVideoSrc: string | undefined;
  videoClipAssetId: string | undefined;
  transitionProgress: number;
  activeSubtitles: StudioSubtitleCue[];
  activeAudioTracks: StudioTimelineTrack[];
  activeAudioCueByTrack: Map<string, { clip: StudioTimelineClip }>;
  signedUrls: Record<string, string>;
  audioRefs: React.MutableRefObject<Record<string, HTMLAudioElement | null>>;
  scrubberRef: React.Ref<HTMLInputElement>;
  timecodeCurRef: React.Ref<HTMLSpanElement>;
  setVideoRef: (el: HTMLVideoElement | null) => void;
  handleAssetMediaError: (assetId: string | undefined, el: HTMLMediaElement) => void;
  playing: boolean;
  onTogglePlaying: () => void;
  durationMs: number;
  clampedPlayheadMs: number;
  seekTo: (ms: number) => void;
}

export const TimelineTransport: React.FC<TimelineTransportProps> = ({
  timeline,
  activeVideoSrc,
  videoClipAssetId,
  transitionProgress,
  activeSubtitles,
  activeAudioTracks,
  activeAudioCueByTrack,
  signedUrls,
  audioRefs,
  scrubberRef,
  timecodeCurRef,
  setVideoRef,
  handleAssetMediaError,
  playing,
  onTogglePlaying,
  durationMs,
  clampedPlayheadMs,
  seekTo,
}) => {
  const t = useT();
  return (
    <Card icon={Play} title={t("studio.timeline.previewTitle")}>
      <div className="timeline-player-stage">
        {activeVideoSrc ? (
          <video
            ref={setVideoRef}
            className="timeline-player-video"
            src={activeVideoSrc}
            preload="auto"
            playsInline
            style={{ opacity: transitionProgress }}
            onError={(e) => handleAssetMediaError(videoClipAssetId, e.currentTarget)}
          />
        ) : (
          <div className="timeline-player-empty">
            {timeline && (timeline.tracks ?? []).some((tr) => (tr.clips ?? []).length > 0)
              ? t("studio.timeline.noClipAtPlayhead")
              : t("studio.timeline.emptyTimeline")}
          </div>
        )}

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

        {activeSubtitles.length > 0 && (
          <div className="timeline-subtitle-overlay">
            {activeSubtitles.map((cue) => (
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
          onClick={onTogglePlaying}
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
          ref={scrubberRef}
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
          <span ref={timecodeCurRef}>{formatTimecodeMs(clampedPlayheadMs)}</span> /{" "}
          {formatTimecodeMs(durationMs)}
        </span>
      </div>
    </Card>
  );
};
