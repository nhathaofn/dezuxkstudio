import React, { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appConfig } from "../../../config/appConfig";
import {
  Actions,
  NodeTypes,
  type StudioEditTimeline,
  type StudioEpisodeDetailResult,
  type StudioTimelineResult,
} from "../../../contracts/generated";
import { rtClient } from "../../../lib/rtClient";
import { useNodeSpecsStore } from "../../workflow/useNodeSpecs";
import { useStudioStore } from "../useStudioStore";
import {
  buildClipTrimOp,
  clipEndMs,
  clipSpanMs,
  collectTimelineAssetIds,
  computeTimelineDurationMs,
  formatTimecodeMs,
  resolvePreviewStateAtTime,
} from "./timelineHelpers";
import {
  ClipInspectorForm,
  ExportNLEDraftDialog,
  RenderTimelineDialog,
  TimelineWorkspace,
} from "./TimelineWorkspace";

function makeSampleTimeline(): StudioEditTimeline {
  return {
    version: 1,
    fps: 24,
    width: 1920,
    height: 1080,
    tracks: [
      {
        id: "v1",
        name: "Video",
        kind: "video",
        muted: false,
        locked: false,
        volume: 1,
        clips: [
          {
            id: "vc1",
            label: "Cảnh 1",
            file_path: "shots/s1.mp4",
            asset_id: "asset-v1",
            sprite_asset_id: "asset-sp1",
            timeline_start_ms: 0,
            source_in_ms: 0,
            source_out_ms: 4000,
            speed: 1,
            volume: 1,
            transition_in: "crossfade",
            transition_ms: 500,
            peaks: [0.2, 0.8, 0.5],
          },
          {
            id: "vc2",
            label: "Cảnh 2 (2x)",
            file_path: "shots/s2.mp4",
            asset_id: "asset-v2",
            timeline_start_ms: 4000,
            source_in_ms: 1000,
            source_out_ms: 5000,
            speed: 2,
            volume: 1,
            transition_in: "none",
            transition_ms: 0,
            peaks: [],
          },
        ],
      },
      {
        id: "a1",
        name: "Voice",
        kind: "audio_narration",
        muted: false,
        solo: false,
        locked: false,
        volume: 1,
        clips: [
          {
            id: "ac1",
            label: "Lời thoại 1",
            file_path: "tts/s1.wav",
            asset_id: "asset-a1",
            timeline_start_ms: 500,
            source_in_ms: 0,
            source_out_ms: 3000,
            speed: 1,
            volume: 0.9,
            transition_in: "none",
            transition_ms: 0,
            peaks: [0.3, 0.9],
          },
        ],
      },
      {
        id: "a2",
        name: "BGM",
        kind: "audio_bgm",
        muted: false,
        solo: false,
        locked: false,
        volume: 0.3,
        clips: [
          {
            id: "bgm1",
            label: "Nhạc nền",
            file_path: "audio/bgm.mp3",
            asset_id: "asset-bgm",
            timeline_start_ms: 0,
            source_in_ms: 0,
            source_out_ms: 7000,
            speed: 1,
            volume: 1,
            transition_in: "none",
            transition_ms: 0,
            peaks: [],
          },
        ],
      },
    ],
    subtitles: [
      {
        id: "sub1",
        start_ms: 400,
        end_ms: 3200,
        speaker: "Minh",
        text: "Đêm nay trời mưa tầm tã.",
      },
    ],
    markers: [],
  };
}

function makeSampleEpisodeDetail(): StudioEpisodeDetailResult {
  return {
    project: {
      id: "proj-1",
      slug: "dem-mua",
      title: "Đêm Mưa",
      genre: "drama",
      production_mode: "storyboard_i2v",
      aspect_ratio: "16:9",
      style_prompt: "Neo-noir",
      root_dir: "/tmp/proj",
      auto_approve_batches: true,
      default_text_model: "",
      default_image_model: "",
      default_video_model: "",
      default_tts_model: "",
      created_at: 1,
      updated_at: 1,
    },
    episode: {
      id: "ep-1",
      project_id: "proj-1",
      code: "ep01",
      title: "Tập 1",
      stage: "export",
      raw_script_path: "",
      script_json_path: "",
      assets_json_path: "",
      shots_json_path: "",
      timeline_json_path: "/tmp/proj/ep01/timeline/timeline.json",
      created_at: 1,
      updated_at: 1,
    },
    assets: [],
    shots: [
      {
        id: "shot-1",
        episode_id: "ep-1",
        seq: 1,
        scene_code: "S01",
        title: "Cảnh 1",
        shot_mode: "single",
        duration_sec: 4,
        image_prompt: "",
        video_prompt: "",
        narration_text: "Đêm nay",
        dialogues: [],
        ref_asset_tags: [],
        keyframe_asset_id: "",
        keyframe_path: "",
        video_asset_id: "asset-v1",
        video_path: "/tmp/proj/ep01/s1.mp4",
        audio_asset_id: "",
        audio_path: "",
        status: "video_ready",
        error_text: "",
        updated_at: 1,
      },
    ],
    raw_script: "",
  };
}

describe("timelineHelpers", () => {
  it("computes speed-adjusted clip span, duration, and timecode formatting", () => {
    const tl = makeSampleTimeline();
    const fastClip = tl.tracks[0].clips[1];
    expect(clipSpanMs(fastClip)).toBe(2000);
    expect(clipEndMs(fastClip)).toBe(6000);
    expect(computeTimelineDurationMs(tl)).toBe(7000);
    expect(formatTimecodeMs(5250)).toBe("00:05.25");
  });

  it("resolves active video, transition progress, audio solo/mute mix, and Vietnamese subtitles", () => {
    const tl = makeSampleTimeline();
    const midTrans = resolvePreviewStateAtTime(tl, 250);
    expect(midTrans.videoClip?.id).toBe("vc1");
    expect(midTrans.transitionType).toBe("crossfade");
    expect(midTrans.transitionProgress).toBeCloseTo(0.5);
    expect(midTrans.audioCues.map((c) => c.trackId)).toEqual(["a2"]);

    const atNarration = resolvePreviewStateAtTime(tl, 1000);
    expect(atNarration.audioCues.map((c) => c.trackId)).toEqual(["a1", "a2"]);
    expect(atNarration.activeSubtitles[0]?.text).toBe("Đêm nay trời mưa tầm tã.");

    // Solo track silences non-solo tracks
    tl.tracks[1].solo = true;
    const soloed = resolvePreviewStateAtTime(tl, 1000);
    expect(soloed.audioCues.map((c) => c.trackId)).toEqual(["a1"]);

    // Fast 2x clip computes doubled source offset
    const atFast = resolvePreviewStateAtTime(tl, 4500);
    expect(atFast.videoClip?.id).toBe("vc2");
    expect(atFast.videoSourceSec).toBeCloseTo(2.0);

    // Clip-level mute silences individual video and audio clips, and volume clamps to maxVolume
    const tlMute = makeSampleTimeline();
    tlMute.tracks[0].clips[0].mute = true;
    tlMute.tracks[1].clips[0].mute = true;
    tlMute.tracks[2].volume = 2;
    tlMute.tracks[2].clips[0].volume = 2;
    const mutedState = resolvePreviewStateAtTime(tlMute, 1000);
    expect(mutedState.videoTrackMuted).toBe(true);
    expect(mutedState.audioCues.map((c) => c.trackId)).toEqual(["a2"]);
    expect(mutedState.audioCues[0].effectiveVolume).toBe(appConfig.studio.timeline.maxVolume);
  });

  it("builds clamped clip trim ops and collects unique media/sprite asset ids", () => {
    const tl = makeSampleTimeline();
    const c1 = tl.tracks[0].clips[0];
    const opIn = buildClipTrimOp(c1, "in", 200);
    expect(opIn).toEqual({
      op: "update_clip",
      clip_id: "vc1",
      timeline_start_ms: 200,
      source_in_ms: 200,
    });
    const opOutClamped = buildClipTrimOp(c1, "out", -10000);
    expect(opOutClamped?.source_out_ms).toBe(250);
    expect(collectTimelineAssetIds(tl)).toEqual([
      "asset-v1",
      "asset-sp1",
      "asset-v2",
      "asset-a1",
      "asset-bgm",
    ]);
  });
});

describe("useStudioStore Phase 3 timeline & export flows", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_sub, getSnapshot) =>
      getSnapshot()
    );
    useStudioStore.setState({
      activeProjectId: "proj-1",
      activeEpisodeId: "ep-1",
      timelineResult: null,
      timelineLoading: false,
      renderingTimeline: false,
      exportingNLE: false,
      lastRenderResult: null,
      lastNLEExport: null,
      episodeDetail: makeSampleEpisodeDetail(),
      error: null,
      saving: false,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches timeline, applies optimistic ops, refreshes on conflict, and handles realtime event", async () => {
    const rev1: StudioTimelineResult = {
      project_id: "proj-1",
      episode_id: "ep-1",
      revision: 1,
      can_undo: false,
      can_redo: false,
      duration_ms: 7000,
      timeline: makeSampleTimeline(),
      revisions: [],
      updated_at: 100,
    };
    const rev2: StudioTimelineResult = {
      ...rev1,
      revision: 2,
      can_undo: true,
    };

    const reqSpy = vi
      .spyOn(rtClient, "request")
      .mockResolvedValueOnce(rev1 as never)
      .mockResolvedValueOnce(rev2 as never)
      .mockRejectedValueOnce({ code: "ERR_CONFLICT", message: "stale revision" } as never)
      .mockResolvedValueOnce(rev2 as never);

    await useStudioStore.getState().fetchTimeline("ep-1");
    expect(useStudioStore.getState().timelineResult?.revision).toBe(1);

    const ok = await useStudioStore
      .getState()
      .applyTimelineOps([{ op: "add_marker", marker: { id: "m1", time_ms: 100, label: "Beat" } }]);
    expect(ok).toBe(true);
    expect(reqSpy).toHaveBeenNthCalledWith(
      2,
      Actions.STUDIO_TIMELINE_APPLY_OPS,
      expect.objectContaining({ episode_id: "ep-1", expected_revision: 1, actor: "user" })
    );
    expect(useStudioStore.getState().timelineResult?.revision).toBe(2);

    // Conflict triggers refresh
    const staleOk = await useStudioStore
      .getState()
      .applyTimelineOps([{ op: "add_marker", marker: { id: "m2", time_ms: 200, label: "Beat2" } }]);
    expect(staleOk).toBe(false);
    expect(useStudioStore.getState().error?.code).toBe("ERR_CONFLICT");

    // Live realtime event updates store
    useStudioStore.getState().receiveTimelineChanged({
      project_id: "proj-1",
      episode_id: "ep-1",
      revision: 3,
      can_undo: true,
      can_redo: false,
      duration_ms: 7000,
      actor: "agent",
      summary: "agent split",
      timeline: makeSampleTimeline(),
    });
    expect(useStudioStore.getState().timelineResult?.revision).toBe(3);
  });

  it("renders MP4 and exports CapCut draft cleanly", async () => {
    const detail = makeSampleEpisodeDetail();
    vi.spyOn(rtClient, "request")
      .mockResolvedValueOnce({
        output_path: "/tmp/proj/ep01/exports/final.mp4",
        asset_id: "asset-final",
        srt_path: "/tmp/proj/ep01/exports/final.srt",
        ass_path: "/tmp/proj/ep01/exports/final.ass",
        duration_sec: 7.0,
        episode: { ...detail, final_video_path: "/tmp/proj/ep01/exports/final.mp4" },
      } as never)
      .mockResolvedValueOnce({
        export_path: "/tmp/proj/ep01/exports/capcut_draft.zip",
        asset_id: "asset-zip",
        format: "capcut",
        file_count: 5,
        total_bytes: 2048,
      } as never);

    const rendered = await useStudioStore
      .getState()
      .renderTimeline({ burn_subtitles: true, loudnorm_lufs: -14 });
    expect(rendered?.asset_id).toBe("asset-final");
    expect(useStudioStore.getState().lastRenderResult?.output_path).toContain("final.mp4");

    const exported = await useStudioStore
      .getState()
      .exportNLEDraft({ format: "capcut", include_media_copy: true });
    expect(exported?.asset_id).toBe("asset-zip");
    expect(useStudioStore.getState().lastNLEExport?.format).toBe("capcut");
  });

  it("renders TimelineWorkspace SSR markup with tracks, waveform, subtitles, and export actions", () => {
    const detail = makeSampleEpisodeDetail();
    useStudioStore.setState({
      activeProjectId: "proj-1",
      activeEpisodeId: "ep-1",
      timelineResult: {
        project_id: "proj-1",
        episode_id: "ep-1",
        revision: 4,
        can_undo: true,
        can_redo: false,
        duration_ms: 7000,
        timeline: makeSampleTimeline(),
        revisions: [],
        updated_at: 100,
      },
      lastNLEExport: {
        export_path: "/tmp/proj/ep01/exports/dem_mua_capcut_draft.zip",
        asset_id: "asset-zip",
        format: "capcut",
        file_count: 5,
        total_bytes: 1024,
      },
    });

    const html = renderToStaticMarkup(createElement(TimelineWorkspace, { detail }));
    expect(html).toContain("Rev 4");
    expect(html).toContain("Cảnh 1");
    expect(html).toContain("Lời thoại 1");
    expect(html).toContain("Đêm nay trời mưa tầm tã.");
    expect(html).toContain("dem_mua_capcut_draft.zip");
    expect(html).toContain("timeline-clip-waveform");
  });

  it("derives RenderTimelineDialog defaults and bounds from tool.render_timeline node spec", () => {
    useNodeSpecsStore.setState({
      specs: [
        {
          type: NodeTypes.RENDER_TIMELINE,
          provider: "studio",
          label: "Timeline FFmpeg Renderer",
          description: "Render timeline",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "subtitle_font_size",
              label: "Font",
              kind: "integer",
              min: 12,
              max: 64,
              default: "30",
            },
            { name: "loudnorm_lufs", label: "LUFS", kind: "text", default: "-16.0" },
            { name: "crf", label: "CRF", kind: "integer", min: 14, max: 32, default: "18" },
            {
              name: "preset",
              label: "Preset",
              kind: "select",
              options: ["veryfast", "medium", "slow"],
              default: "veryfast",
            },
          ],
        },
      ],
    });

    const html = renderToStaticMarkup(
      createElement(RenderTimelineDialog, {
        defaultFilename: "ep01_custom.mp4",
        onClose: () => {},
      })
    );
    expect(html).toContain('value="30"');
    expect(html).toContain('max="64"');
    expect(html).toContain('value="-16"');
    expect(html).toContain('value="18"');
    expect(html).toContain('value="veryfast"');
  });

  it("omits hardcoded RenderTimelineDialog bounds when specs are empty and normalizes omitted timeline slices", async () => {
    useNodeSpecsStore.setState({ specs: [] });
    const emptySpecHtml = renderToStaticMarkup(
      createElement(RenderTimelineDialog, {
        defaultFilename: "ep01_empty_spec.mp4",
        onClose: () => {},
      })
    );
    expect(emptySpecHtml).not.toContain('max="72"');
    expect(emptySpecHtml).not.toContain('value="20"');
    expect(emptySpecHtml).not.toContain('value="-14"');

    // Boundary normalizer handles omitted/undefined omitempty arrays on StudioTimelineResult
    vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      project_id: "proj-1",
      episode_id: "ep-1",
      revision: 9,
      can_undo: false,
      can_redo: false,
      duration_ms: 0,
      timeline: {
        version: 1,
        fps: 24,
        width: 1920,
        height: 1080,
        tracks: [
          {
            id: "v1",
            kind: "video",
            muted: false,
            locked: false,
            volume: 1,
            clips: undefined as unknown as StudioEditTimeline["tracks"][number]["clips"],
          },
        ],
        subtitles: undefined as unknown as StudioEditTimeline["subtitles"],
        markers: undefined,
      },
      updated_at: 200,
    } as never);

    await useStudioStore.getState().fetchTimeline("ep-1");
    const fetched = useStudioStore.getState().timelineResult?.timeline;
    expect(fetched?.tracks[0].clips).toEqual([]);
    expect(fetched?.subtitles).toEqual([]);
    expect(fetched?.markers).toEqual([]);

    const safePreview = resolvePreviewStateAtTime(
      {
        version: 1,
        fps: 24,
        width: 1920,
        height: 1080,
        tracks: undefined as unknown as StudioEditTimeline["tracks"],
        subtitles: undefined as unknown as StudioEditTimeline["subtitles"],
        markers: undefined as unknown as StudioEditTimeline["markers"],
      },
      0
    );
    expect(safePreview.videoClip).toBeNull();
    expect(safePreview.activeSubtitles).toEqual([]);
  });

  it("reads burn_subtitles, ExportNLEDraftDialog, and ClipInspectorForm bounds strictly from workflow:specs", () => {
    useNodeSpecsStore.setState({ specs: [] });
    const sampleClip = makeSampleTimeline().tracks[0].clips[0];

    // Empty specs: burn_subtitles is false, NLE has no hardcoded capcut, ClipInspectorForm has no max="3000"
    const emptyRenderHtml = renderToStaticMarkup(
      createElement(RenderTimelineDialog, { defaultFilename: "out.mp4", onClose: () => {} })
    );
    expect(emptyRenderHtml).toContain('aria-checked="false"');

    const emptyNleHtml = renderToStaticMarkup(
      createElement(ExportNLEDraftDialog, { defaultProjectName: "proj", onClose: () => {} })
    );
    expect(emptyNleHtml).not.toContain('value="capcut"');

    const emptyInspectorHtml = renderToStaticMarkup(
      createElement(ClipInspectorForm, {
        clip: sampleClip,
        locked: false,
        busy: false,
        canSplit: false,
        playheadMs: 0,
        loadingPeaks: false,
        onApplyOps: async () => true,
        onComputePeaks: async () => null,
        onDeleted: () => {},
      })
    );
    expect(emptyInspectorHtml).not.toContain('max="3000"');

    // Loaded specs populate burn_subtitles, NLE format/include_media_copy, and transition_ms max
    useNodeSpecsStore.setState({
      specs: [
        {
          type: NodeTypes.RENDER_TIMELINE,
          provider: "studio",
          label: "Render",
          description: "Render",
          inputs: [],
          outputs: [],
          params: [
            { name: "burn_subtitles", label: "Burn", kind: "toggle", default: "false" },
            { name: "overwrite", label: "Overwrite", kind: "toggle", default: "true" },
          ],
        },
        {
          type: NodeTypes.EXPORT_NLE_DRAFT,
          provider: "studio",
          label: "Export NLE",
          description: "Export",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "format",
              label: "Format",
              kind: "select",
              options: ["otio", "fcpxml", "capcut"],
              default: "otio",
            },
            { name: "include_media_copy", label: "Copy", kind: "toggle", default: "false" },
          ],
        },
        {
          type: NodeTypes.TIMELINE_AUTO_ASSEMBLE,
          provider: "studio",
          label: "Assemble",
          description: "Assemble",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "transition_ms",
              label: "Transition",
              kind: "integer",
              min: 0,
              max: 2400,
              default: "500",
            },
          ],
        },
      ],
    });

    const specNleHtml = renderToStaticMarkup(
      createElement(ExportNLEDraftDialog, { defaultProjectName: "proj", onClose: () => {} })
    );
    expect(specNleHtml).toContain('value="otio" selected=""');

    const specInspectorHtml = renderToStaticMarkup(
      createElement(ClipInspectorForm, {
        clip: sampleClip,
        locked: false,
        busy: false,
        canSplit: false,
        playheadMs: 0,
        loadingPeaks: false,
        onApplyOps: async () => true,
        onComputePeaks: async () => null,
        onDeleted: () => {},
      })
    );
    expect(specInspectorHtml).toContain('max="2400"');
  });

  it("recovers via fetchTimeline on ERR_CONFLICT in undo, redo, and autoAssemble, and guards receiveTimelineChanged", async () => {
    const syncedRes: StudioTimelineResult = {
      project_id: "proj-1",
      episode_id: "ep-1",
      revision: 12,
      can_undo: true,
      can_redo: false,
      duration_ms: 7000,
      timeline: makeSampleTimeline(),
      revisions: [],
      updated_at: 500,
    };

    useStudioStore.setState({
      activeProjectId: "proj-1",
      activeEpisodeId: "ep-1",
      timelineResult: { ...syncedRes, revision: 10 },
    });

    const spy = vi.spyOn(rtClient, "request");
    for (const runConflictAction of [
      () => useStudioStore.getState().undoTimeline(),
      () => useStudioStore.getState().redoTimeline(),
      () => useStudioStore.getState().autoAssembleTimeline(),
    ]) {
      useStudioStore.setState({ timelineResult: { ...syncedRes, revision: 10 }, error: null });
      spy
        .mockRejectedValueOnce({ code: "ERR_CONFLICT", message: "stale" } as never)
        .mockResolvedValueOnce(syncedRes as never);

      const ok = await runConflictAction();
      expect(ok).toBe(false);
      await Promise.resolve();
      await Promise.resolve();
      expect(useStudioStore.getState().error?.code).toBe("ERR_CONFLICT");
      expect(useStudioStore.getState().timelineResult?.revision).toBe(12);
    }

    // When activeEpisodeId is null, receiveTimelineChanged must ignore stray events
    useStudioStore.setState({ activeEpisodeId: null, timelineResult: null });
    useStudioStore.getState().receiveTimelineChanged({
      project_id: "proj-1",
      episode_id: "ep-other",
      revision: 99,
      can_undo: false,
      can_redo: false,
      duration_ms: 1000,
      actor: "agent",
      summary: "stray",
      timeline: makeSampleTimeline(),
    });
    expect(useStudioStore.getState().timelineResult).toBeNull();
  });
});
