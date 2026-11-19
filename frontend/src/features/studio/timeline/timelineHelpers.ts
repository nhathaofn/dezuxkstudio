import { appConfig } from "../../../config/appConfig";
import type {
  StudioEditTimeline,
  StudioSubtitleCue,
  StudioTimelineClip,
  StudioTimelineOp,
  StudioTimelineOpKind,
  StudioTimelineTrack,
  StudioTrackKind,
  StudioTransitionType,
} from "../../../contracts/generated";

export function clipSpanMs(clip: StudioTimelineClip): number {
  const srcSpan = Math.max(0, clip.source_out_ms - clip.source_in_ms);
  if (srcSpan <= 0) return 0;
  const spd = clip.speed > 0 ? clip.speed : 1;
  return Math.max(1, Math.round(srcSpan / spd));
}

export function clipEndMs(clip: StudioTimelineClip): number {
  return clip.timeline_start_ms + clipSpanMs(clip);
}

export function computeTimelineDurationMs(timeline?: StudioEditTimeline | null): number {
  if (!timeline) return 0;
  let maxMs = 0;
  for (const tr of timeline.tracks ?? []) {
    for (const cl of tr.clips ?? []) {
      const end = clipEndMs(cl);
      if (end > maxMs) maxMs = end;
    }
  }
  for (const sub of timeline.subtitles ?? []) {
    if (sub.end_ms > maxMs) maxMs = sub.end_ms;
  }
  return maxMs;
}

export function formatTimecodeMs(ms: number): string {
  const safe = Math.max(0, Math.round(ms));
  const totalSec = Math.floor(safe / 1000);
  const mins = Math.floor(totalSec / 60);
  const secs = totalSec % 60;
  const centis = Math.floor((safe % 1000) / 10);
  return `${String(mins).padStart(2, "0")}:${String(secs).padStart(2, "0")}.${String(centis).padStart(2, "0")}`;
}

export function isAudioTrackKind(kind: StudioTrackKind): boolean {
  return (
    kind === "audio_dialogue" ||
    kind === "audio_narration" ||
    kind === "audio_bgm" ||
    kind === "audio_sfx"
  );
}

export function findClipInTimeline(
  timeline: StudioEditTimeline | null | undefined,
  clipId: string | null
): { track: StudioTimelineTrack; clip: StudioTimelineClip } | null {
  if (!timeline || !clipId) return null;
  for (const tr of timeline.tracks ?? []) {
    for (const cl of tr.clips ?? []) {
      if (cl.id === clipId) return { track: tr, clip: cl };
    }
  }
  return null;
}

export interface ActiveAudioCue {
  trackId: string;
  clip: StudioTimelineClip;
  sourceSec: number;
  effectiveVolume: number;
}

export interface ResolvedPreviewState {
  videoClip: StudioTimelineClip | null;
  videoTrackMuted: boolean;
  videoSourceSec: number;
  transitionType: StudioTransitionType;
  transitionProgress: number;
  audioCues: ActiveAudioCue[];
  activeSubtitles: StudioSubtitleCue[];
}

export function resolvePreviewStateAtTime(
  timeline: StudioEditTimeline | null | undefined,
  playheadMs: number
): ResolvedPreviewState {
  if (!timeline) {
    return {
      videoClip: null,
      videoTrackMuted: false,
      videoSourceSec: 0,
      transitionType: "none",
      transitionProgress: 1,
      audioCues: [],
      activeSubtitles: [],
    };
  }

  const tracks = timeline.tracks ?? [];
  const anySolo = tracks.some((tr) => tr.solo);

  let videoClip: StudioTimelineClip | null = null;
  let videoTrackMuted = false;
  let videoSourceSec = 0;
  let transitionType: StudioTransitionType = "none";
  let transitionProgress = 1;
  const audioCues: ActiveAudioCue[] = [];

  for (const tr of tracks) {
    const audible = anySolo ? Boolean(tr.solo) : !tr.muted;
    if (tr.kind === "video") {
      if (tr.muted) {
        videoTrackMuted = true;
      }
      if (!videoClip) {
        const hit = (tr.clips ?? []).find(
          (cl) => playheadMs >= cl.timeline_start_ms && playheadMs < clipEndMs(cl)
        );
        if (hit) {
          videoClip = hit;
          videoTrackMuted = !audible || Boolean(hit.mute);
          const spd = hit.speed > 0 ? hit.speed : 1;
          const offsetMs = Math.max(0, playheadMs - hit.timeline_start_ms);
          videoSourceSec = (hit.source_in_ms + offsetMs * spd) / 1000;
          transitionType = hit.transition_in;
          if (
            hit.transition_in !== "none" &&
            hit.transition_ms > 0 &&
            offsetMs < hit.transition_ms
          ) {
            transitionProgress = Math.max(0, Math.min(1, offsetMs / hit.transition_ms));
          }
        }
      }
      continue;
    }

    if (isAudioTrackKind(tr.kind) && audible) {
      for (const cl of tr.clips ?? []) {
        if (cl.mute) continue;
        if (playheadMs >= cl.timeline_start_ms && playheadMs < clipEndMs(cl)) {
          const spd = cl.speed > 0 ? cl.speed : 1;
          const offsetMs = Math.max(0, playheadMs - cl.timeline_start_ms);
          audioCues.push({
            trackId: tr.id,
            clip: cl,
            sourceSec: (cl.source_in_ms + offsetMs * spd) / 1000,
            effectiveVolume: Math.max(
              0,
              Math.min(appConfig.studio.timeline.maxVolume, tr.volume * cl.volume)
            ),
          });
        }
      }
    }
  }

  const activeSubtitles = (timeline.subtitles ?? []).filter(
    (sub) => playheadMs >= sub.start_ms && playheadMs <= sub.end_ms
  );

  return {
    videoClip,
    videoTrackMuted,
    videoSourceSec,
    transitionType,
    transitionProgress,
    audioCues,
    activeSubtitles,
  };
}

export function buildClipTrimOp(
  clip: StudioTimelineClip,
  edge: "in" | "out",
  deltaSrcMs: number
): StudioTimelineOp | null {
  const minSpanMs = appConfig.studio.timeline.minTrimSpanMs;
  const spd = clip.speed > 0 ? clip.speed : 1;
  if (edge === "in") {
    const nextIn = Math.max(
      0,
      Math.min(clip.source_out_ms - minSpanMs, clip.source_in_ms + deltaSrcMs)
    );
    const appliedDeltaSrc = nextIn - clip.source_in_ms;
    if (appliedDeltaSrc === 0) return null;
    const nextStart = Math.max(0, clip.timeline_start_ms + Math.round(appliedDeltaSrc / spd));
    return {
      op: "update_clip" satisfies StudioTimelineOpKind,
      clip_id: clip.id,
      timeline_start_ms: nextStart,
      source_in_ms: nextIn,
    };
  }
  const nextOut = Math.max(clip.source_in_ms + minSpanMs, clip.source_out_ms + deltaSrcMs);
  if (nextOut === clip.source_out_ms) return null;
  return {
    op: "update_clip" satisfies StudioTimelineOpKind,
    clip_id: clip.id,
    source_out_ms: nextOut,
  };
}

export function collectTimelineAssetIds(timeline: StudioEditTimeline | null | undefined): string[] {
  if (!timeline) return [];
  const seen = new Set<string>();
  for (const tr of timeline.tracks ?? []) {
    for (const cl of tr.clips ?? []) {
      if (cl.asset_id) seen.add(cl.asset_id);
      if (cl.sprite_asset_id) seen.add(cl.sprite_asset_id);
    }
  }
  return Array.from(seen);
}
