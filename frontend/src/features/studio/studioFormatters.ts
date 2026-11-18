import { appConfig } from "../../config/appConfig";
import type {
  BatchPreflightOutput,
  StudioCostSummaryResult,
  StudioEditTimeline,
  StudioEpisodeDetailResult,
  StudioProjectDetailResult,
  StudioQCReport,
  StudioTimelineResult,
} from "../../contracts/generated";

/** Locale-aware currency formatter respecting server-provided ISO currency codes. */
export function formatUSD(
  amount: number,
  digits: number = appConfig.studio.ui.detailUsdDigits,
  currency = "USD",
  locale = "en-US"
): string {
  const value = Number.isFinite(amount) ? amount : 0;
  const safeCurrency = (currency || "USD").trim().toUpperCase() || "USD";
  try {
    return new Intl.NumberFormat(locale, {
      style: "currency",
      currency: safeCurrency,
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    }).format(value);
  } catch {
    return `${safeCurrency} ${value.toFixed(digits)}`;
  }
}

/** Ensures omitempty/nullable arrays on EditTimeline are normalized to non-null slices at the client boundary. */
export function normalizeTimeline(
  timeline: StudioEditTimeline | null | undefined
): StudioEditTimeline {
  return {
    version: timeline?.version ?? 1,
    fps: timeline?.fps ?? 0,
    width: timeline?.width ?? 0,
    height: timeline?.height ?? 0,
    tracks: (timeline?.tracks ?? []).map((tr) => ({
      ...tr,
      clips: (tr.clips ?? []).map((cl) => ({
        ...cl,
        peaks: cl.peaks ?? [],
      })),
    })),
    subtitles: timeline?.subtitles ?? [],
    markers: timeline?.markers ?? [],
  };
}

/** Ensures omitempty/nullable slices on StudioTimelineResult are normalized at the client boundary. */
export function normalizeTimelineResult(res: StudioTimelineResult): StudioTimelineResult {
  return {
    ...res,
    timeline: normalizeTimeline(res.timeline),
    revisions: res.revisions ?? [],
  };
}

/** Ensures omitempty/nullable maps and arrays on StudioCostSummaryResult are normalized at the client boundary. */
export function normalizeCostSummary(sum: StudioCostSummaryResult): StudioCostSummaryResult {
  return {
    ...sum,
    currency: sum.currency || "USD",
    by_capability: sum.by_capability ?? {},
    by_provider: sum.by_provider ?? {},
    entries: sum.entries ?? [],
    pricing: sum.pricing ?? [],
  };
}

/** Ensures omitempty/nullable arrays on StudioQCReport are normalized at the client boundary. */
export function normalizeQCReport(rep: StudioQCReport): StudioQCReport {
  return {
    ...rep,
    issues: rep.issues ?? [],
    sample_frames: rep.sample_frames ?? [],
  };
}

/** Ensures omitempty/nullable warning and blocker arrays on BatchPreflightOutput are normalized at the client boundary. */
export function normalizePreflight(pf: BatchPreflightOutput): BatchPreflightOutput {
  return {
    ...pf,
    warnings: pf.warnings ?? [],
    warning_items: pf.warning_items ?? [],
    blockers: pf.blockers ?? [],
    blocker_items: pf.blocker_items ?? [],
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
