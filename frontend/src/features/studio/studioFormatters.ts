import type {
  StudioEditTimeline,
  StudioEpisodeDetailResult,
  StudioProjectDetailResult,
} from "../../contracts/generated";

/** Locale-aware currency formatter using Intl.NumberFormat instead of raw currency prefix literals. */
export function formatUSD(amount: number, digits = 4): string {
  const value = Number.isFinite(amount) ? amount : 0;
  try {
    return new Intl.NumberFormat("en-US", {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    }).format(value);
  } catch {
    return `$${value.toFixed(digits)}`;
  }
}

/** Ensures omitempty/nullable arrays on EditTimeline are normalized to non-null slices at the client boundary. */
export function normalizeTimeline(timeline: StudioEditTimeline): StudioEditTimeline {
  return {
    ...timeline,
    tracks: (timeline.tracks ?? []).map((tr) => ({
      ...tr,
      clips: tr.clips ?? [],
    })),
    subtitles: timeline.subtitles ?? [],
    markers: timeline.markers ?? [],
  };
}

/** Ensures omitempty/nullable arrays on StudioEpisodeDetailResult are normalized at the client boundary. */
export function normalizeEpisodeDetail(
  detail: StudioEpisodeDetailResult
): StudioEpisodeDetailResult {
  return {
    ...detail,
    assets: detail.assets ?? [],
    shots: (detail.shots ?? []).map((sh) => ({
      ...sh,
      dialogues: sh.dialogues ?? [],
      ref_asset_tags: sh.ref_asset_tags ?? [],
    })),
    normalized_script: detail.normalized_script
      ? {
          ...detail.normalized_script,
          characters: detail.normalized_script.characters ?? [],
          scenes: (detail.normalized_script.scenes ?? []).map((sc) => ({
            ...sc,
            beats: (sc.beats ?? []).map((bt) => ({
              ...bt,
              asset_tags: bt.asset_tags ?? [],
              dialogues: bt.dialogues ?? [],
              narrations: bt.narrations ?? [],
            })),
          })),
        }
      : detail.normalized_script,
  };
}

/** Ensures omitempty/nullable arrays on StudioProjectDetailResult are normalized at the client boundary. */
export function normalizeProjectDetail(
  detail: StudioProjectDetailResult
): StudioProjectDetailResult {
  return {
    ...detail,
    episodes: detail.episodes ?? [],
    assets: detail.assets ?? [],
    active_episode: detail.active_episode
      ? normalizeEpisodeDetail(detail.active_episode)
      : detail.active_episode,
  };
}
