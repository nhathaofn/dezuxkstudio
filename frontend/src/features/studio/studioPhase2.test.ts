import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Actions,
  type BatchPreflightOutput,
  type StudioAsset,
  type StudioCostSummaryResult,
  type StudioEpisodeDetailResult,
  type StudioJob,
  type StudioMediaVersion,
  type StudioProject,
  type StudioQCReport,
} from "../../contracts/generated";
import { rtClient } from "../../lib/rtClient";
import { useAgentStore } from "../agent/useAgentStore";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useNodeSpecsStore } from "../workflow/useNodeSpecs";
import { buildMaskDataURLFromStrokes, computeGridDividerPercents } from "./phase2Helpers";
import {
  StudioGridSplitterModal,
  StudioMaskEditorModal,
  StudioPreflightDialog,
  StudioVersionDrawer,
} from "./StudioPhase2Modals";
import { StudioQueueCostDrawer } from "./StudioQueueCostDrawer";
import { StudioView } from "./StudioView";
import { targetVersionKey, useStudioStore } from "./useStudioStore";

vi.mock("react-router-dom", async (importOriginal) => {
  const mod = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...mod,
    useNavigate: () => vi.fn(),
  };
});

function makeProject(autoApprove = false): StudioProject {
  return {
    id: "proj-p2",
    slug: "phase2-film",
    title: "Phase 2 Film",
    genre: "drama",
    production_mode: "storyboard_i2v",
    aspect_ratio: "16:9",
    style_prompt: "anamorphic sci-fi",
    root_dir: "D:/studio/phase2-film",
    auto_approve_batches: autoApprove,
    default_text_model: "",
    default_image_model: "",
    default_video_model: "",
    default_tts_model: "",
    created_at: 1000,
    updated_at: 1000,
  };
}

const characterAsset: StudioAsset = {
  id: "asset-char-1",
  project_id: "proj-p2",
  kind: "character",
  name: "Kai",
  code_tag: "CHAR_KAI",
  description: "Cybernetic pilot",
  prompt: "cybernetic pilot portrait, neutral lighting",
  primary_asset_id: "lib-asset-1",
  primary_path: "D:/studio/assets/kai.png",
  voice_provider: "openai",
  voice_id: "alloy",
  voice_style: "calm",
  updated_at: 1000,
};

function makeEpisodeDetail(autoApprove = false): StudioEpisodeDetailResult {
  const proj = makeProject(autoApprove);
  return {
    project: proj,
    episode: {
      id: "ep-1",
      project_id: proj.id,
      code: "ep01",
      title: "Episode 1",
      stage: "video",
      raw_script_path: "D:/studio/ep01/script.txt",
      script_json_path: "D:/studio/ep01/script.json",
      assets_json_path: "D:/studio/ep01/assets.json",
      shots_json_path: "D:/studio/ep01/shots.json",
      timeline_json_path: "D:/studio/ep01/timeline.json",
      created_at: 1000,
      updated_at: 1000,
    },
    raw_script: "INT. HANGAR - NIGHT",
    assets: [characterAsset],
    shots: [
      {
        id: "shot-1",
        episode_id: "ep-1",
        seq: 1,
        scene_code: "sc01",
        title: "Shot 1",
        shot_mode: "single",
        duration_sec: 4,
        ref_asset_tags: ["CHAR_KAI"],
        image_prompt: "Kai enters hangar",
        video_prompt: "slow dolly forward",
        narration_text: "I am ready.",
        keyframe_asset_id: "lib-kf-1",
        keyframe_path: "D:/studio/kf/shot1.png",
        video_asset_id: "lib-vid-1",
        video_path: "D:/studio/vid/shot1.mp4",
        audio_asset_id: "lib-aud-1",
        audio_path: "D:/studio/aud/shot1.mp3",
        status: "video_ready",
        error_text: "",
        updated_at: 1000,
      },
      {
        id: "shot-2",
        episode_id: "ep-1",
        seq: 2,
        scene_code: "sc01",
        title: "Shot 2",
        shot_mode: "single",
        duration_sec: 5,
        ref_asset_tags: ["CHAR_KAI"],
        image_prompt: "Cockpit ignition",
        video_prompt: "hud lights up",
        narration_text: "Launch sequence started.",
        keyframe_asset_id: "",
        keyframe_path: "",
        video_asset_id: "",
        video_path: "",
        audio_asset_id: "",
        audio_path: "",
        status: "pending",
        error_text: "",
        updated_at: 1000,
      },
    ],
  };
}

const samplePreflight: BatchPreflightOutput = {
  ready: true,
  provider_id: "openai",
  model_id: "sora-2",
  capability: "video",
  item_count: 2,
  unit: "second",
  unit_price_usd: 0.08,
  estimated_usd: 0.72,
  estimated_sec: 9,
  warnings: ["High motion complexity"],
};

const sampleJob: StudioJob = {
  id: "job-1",
  project_id: "proj-p2",
  episode_id: "ep-1",
  shot_id: "shot-2",
  job_kind: "shot_video",
  provider_id: "openai",
  model_ref: "openai/sora-2",
  status: "polling",
  provider_job_id: "remote-123",
  progress: 50,
  attempts: 1,
  max_attempts: 5,
  estimated_usd: 0.4,
  actual_usd: 0.4,
  created_at: 1000,
  updated_at: 1010,
};

const sampleCostSummary: StudioCostSummaryResult = {
  project_id: "proj-p2",
  episode_id: "ep-1",
  currency: "USD",
  estimated_total_usd: 0.72,
  actual_total_usd: 0.4,
  entry_count: 1,
  by_capability: { video: 0.4 },
  by_provider: { openai: 0.4 },
  entries: [],
  pricing: [
    {
      id: "pr-1",
      provider_id: "openai",
      model_id: "sora-2",
      capability: "video",
      unit: "second",
      unit_price_usd: 0.08,
      currency: "USD",
      updated_at: 1000,
    },
  ],
};

describe("Studio Phase 2 ArcReel helpers, store & modals", () => {
  beforeEach(() => {
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
      getSnapshot()
    );
    const detail = makeEpisodeDetail(false);
    useStudioStore.setState({
      projects: [detail.project],
      projectsDir: "D:/studio",
      projectDetail: {
        project: detail.project,
        episodes: [detail.episode],
        assets: detail.assets,
        active_episode: detail,
      },
      episodeDetail: detail,
      activeProjectId: detail.project.id,
      activeEpisodeId: detail.episode.id,
      activeStage: "video",
      loadingProjects: false,
      loadingEpisode: false,
      saving: false,
      runningStage: null,
      error: null,
      actionProgress: null,
      batchProgress: null,
      probeMap: {},
      jobs: [],
      costSummary: null,
      versionsByTarget: {},
      qcByTarget: {},
      queueDrawerOpen: false,
      versionDrawerTarget: null,
      gridModalTarget: null,
      maskModalTarget: null,
      pendingPreflight: null,
    });
    useAgentStore.setState({ approvals: [] });
    useNodeSpecsStore.setState({ specs: [], loading: false, error: null });
    usePreferencesStore.setState({ prefs: { theme: "light", language: "en" } });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("computes normalized grid divider percentages matching split weights", () => {
    expect(computeGridDividerPercents([1])).toEqual([]);
    expect(computeGridDividerPercents([1, 1])).toEqual([50]);
    expect(computeGridDividerPercents([1, 2, 1])).toEqual([25, 75]);
    expect(computeGridDividerPercents([0, 3])).toEqual([25]);
  });

  it("renders pure black/white HTML5 mask PNG DataURL from brush strokes", () => {
    const ops: string[] = [];
    const mockCtx = {
      fillStyle: "",
      strokeStyle: "",
      lineCap: "",
      lineJoin: "",
      lineWidth: 0,
      fillRect: vi.fn((x: number, y: number, w: number, h: number) => {
        ops.push(`fillRect(${x},${y},${w},${h})@${mockCtx.fillStyle}`);
      }),
      beginPath: vi.fn(() => ops.push("beginPath")),
      moveTo: vi.fn((x: number, y: number) => ops.push(`moveTo(${x},${y})`)),
      lineTo: vi.fn((x: number, y: number) => ops.push(`lineTo(${x},${y})`)),
      stroke: vi.fn(() => ops.push(`stroke@${mockCtx.strokeStyle}:${mockCtx.lineWidth}`)),
    };
    const mockCanvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => mockCtx),
      toDataURL: vi.fn(() => "data:image/png;base64,MOCKMASK"),
    };
    vi.stubGlobal("document", {
      createElement: vi.fn(() => mockCanvas),
    });

    const dataUrl = buildMaskDataURLFromStrokes(512, 256, [
      {
        size: 24,
        points: [
          { x: 10, y: 20 },
          { x: 100, y: 120 },
        ],
      },
      { size: 12, points: [{ x: 40, y: 50 }] },
    ]);

    expect(dataUrl).toBe("data:image/png;base64,MOCKMASK");
    expect(mockCanvas.width).toBe(512);
    expect(mockCanvas.height).toBe(256);
    expect(ops[0]).toBe("fillRect(0,0,512,256)@#000000");
    expect(ops).toContain("stroke@#ffffff:24");
    expect(ops).toContain("stroke@#ffffff:12");
  });

  it("enforces approval gate via preflight before runStage and enqueueBatch when auto_approve_batches is false", async () => {
    const spy = vi.spyOn(rtClient, "request").mockImplementation((async (action: string) => {
      if (action === Actions.STUDIO_BATCH_PREFLIGHT) {
        return samplePreflight;
      }
      if (action === Actions.STUDIO_EPISODE_RUN_STAGE) {
        return { episode: makeEpisodeDetail(false), summary: "generated" };
      }
      if (action === Actions.STUDIO_QUEUE_ENQUEUE) {
        return { jobs: [sampleJob], skipped: 0, estimated_usd: 0.72, summary: "queued" };
      }
      if (action === Actions.STUDIO_COST_SUMMARY) {
        return sampleCostSummary;
      }
      return {};
    }) as typeof rtClient.request);

    // 1. runStage blocks on preflight when auto_approve_batches === false
    await useStudioStore.getState().runStage("generate_videos", { model: "openai/sora-2" });
    expect(spy).toHaveBeenCalledWith(
      Actions.STUDIO_BATCH_PREFLIGHT,
      expect.objectContaining({
        project_id: "proj-p2",
        episode_id: "ep-1",
        stage: "generate_videos",
      })
    );
    expect(spy).not.toHaveBeenCalledWith(Actions.STUDIO_EPISODE_RUN_STAGE, expect.anything());
    expect(useStudioStore.getState().pendingPreflight?.mode).toBe("stage");

    // Confirm preflight -> passes approved: true
    await useStudioStore.getState().confirmPreflight();
    expect(spy).toHaveBeenCalledWith(
      Actions.STUDIO_EPISODE_RUN_STAGE,
      expect.objectContaining({ episode_id: "ep-1", stage: "generate_videos", approved: true }),
      expect.any(Object)
    );
    expect(useStudioStore.getState().pendingPreflight).toBeNull();

    // 2. enqueueBatch also blocks on preflight until confirmed
    await useStudioStore.getState().enqueueBatch({ stage: "generate_videos" });
    expect(spy).not.toHaveBeenCalledWith(Actions.STUDIO_QUEUE_ENQUEUE, expect.anything());
    expect(useStudioStore.getState().pendingPreflight?.mode).toBe("queue");

    await useStudioStore.getState().confirmPreflight();
    expect(spy).toHaveBeenCalledWith(
      Actions.STUDIO_QUEUE_ENQUEUE,
      expect.objectContaining({ episode_id: "ep-1", stage: "generate_videos", approved: true })
    );
    expect(useStudioStore.getState().jobs).toHaveLength(1);

    // 3. When auto_approve_batches is true, enqueueBatch bypasses preflight confirmation modal
    spy.mockClear();
    const autoDetail = makeEpisodeDetail(true);
    useStudioStore.setState({
      projectDetail: {
        project: autoDetail.project,
        episodes: [autoDetail.episode],
        assets: autoDetail.assets,
        active_episode: autoDetail,
      },
      episodeDetail: autoDetail,
    });
    await useStudioStore.getState().enqueueBatch({ stage: "lipsync" });
    expect(spy).toHaveBeenCalledWith(
      Actions.STUDIO_QUEUE_ENQUEUE,
      expect.objectContaining({ episode_id: "ep-1", stage: "lipsync" })
    );
    expect(useStudioStore.getState().pendingPreflight).toBeNull();
  });

  it("handles queue control, realtime queue/cost events, versions, grid split, mask edit, frame extract, QC & voice binding", async () => {
    const version1: StudioMediaVersion = {
      id: "ver-1",
      project_id: "proj-p2",
      episode_id: "ep-1",
      target_kind: "shot_keyframe",
      target_id: "shot-1",
      version_num: 1,
      media_path: "D:/studio/kf/shot1_v1.png",
      source_node: "tool.mask_edit",
      cost_usd: 0.04,
      qc_status: "pass",
      is_selected: true,
      created_at: 1005,
    };
    const qcReport: StudioQCReport = {
      id: "qc-1",
      project_id: "proj-p2",
      episode_id: "ep-1",
      target_kind: "shot_video",
      target_id: "shot-1",
      media_path: "D:/studio/vid/shot1.mp4",
      verdict: "pass",
      overall_score: 9.2,
      issues: [],
      vision_summary: "Sharp lighting and consistent silhouette.",
      created_at: 1008,
    };

    const spy = vi.spyOn(rtClient, "request").mockImplementation((async (
      action: string,
      payload: Record<string, unknown>
    ) => {
      switch (action) {
        case Actions.STUDIO_QUEUE_CONTROL:
          return { jobs: [{ ...sampleJob, status: "paused" }], updated: 1 };
        case Actions.STUDIO_QUEUE_LIST:
          return { jobs: [{ ...sampleJob, status: "paused" }] };
        case Actions.STUDIO_COST_SUMMARY:
          return sampleCostSummary;
        case Actions.STUDIO_VERSIONS_LIST:
          return { versions: [version1], qc_reports: [qcReport] };
        case Actions.STUDIO_VERSION_SELECT:
          return { version: { ...version1, is_selected: true }, episode: makeEpisodeDetail(false) };
        case Actions.STUDIO_GRID_SPLIT:
          return {
            panels: [
              {
                index: 0,
                row: 0,
                col: 0,
                path: "D:/studio/split/p0.png",
                width: 512,
                height: 512,
                asset_id: "lib-p0",
              },
            ],
            versions: [version1],
          };
        case Actions.STUDIO_MASK_EDIT:
          return {
            path: "D:/studio/kf/shot1_masked.png",
            asset_id: "lib-masked",
            version: version1,
          };
        case Actions.STUDIO_EXTRACT_LAST_FRAME:
          return {
            frame_path: "D:/studio/kf/shot2_from_last.png",
            asset_id: "lib-kf-2",
            version: { ...version1, target_id: "shot-2" },
            episode: makeEpisodeDetail(false),
          };
        case Actions.STUDIO_QC_REVIEW:
          return qcReport;
        case Actions.STUDIO_ASSET_UPSERT:
          return {
            assets: [
              {
                ...characterAsset,
                voice_provider: String(payload.voice_provider ?? ""),
                voice_id: String(payload.voice_id ?? ""),
                voice_style: String(payload.voice_style ?? ""),
              },
            ],
          };
        default:
          return {};
      }
    }) as typeof rtClient.request);

    const store = useStudioStore.getState();

    // Realtime queue & cost events
    store.receiveQueueUpdated({ project_id: "proj-p2", job: sampleJob });
    expect(useStudioStore.getState().jobs).toHaveLength(1);
    store.receiveCostUpdated({
      project_id: "proj-p2",
      episode_id: "ep-1",
      actual_total_usd: 0.4,
      estimated_total_usd: 0.72,
      entry_count: 1,
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(useStudioStore.getState().costSummary?.actual_total_usd).toBe(0.4);

    // Queue pause control
    await store.controlQueue({ action: "pause", job_id: "job-1" });
    expect(useStudioStore.getState().jobs[0].status).toBe("paused");

    // Versions + Select + QC
    await store.fetchVersions("shot_keyframe", "shot-1");
    expect(
      useStudioStore.getState().versionsByTarget[targetVersionKey("shot_keyframe", "shot-1")]
    ).toHaveLength(1);
    await store.selectVersion("ver-1");
    expect(spy).toHaveBeenCalledWith(Actions.STUDIO_VERSION_SELECT, { version_id: "ver-1" });

    // Grid split + Mask edit
    const splitOk = await store.splitGrid({
      project_id: "proj-p2",
      episode_id: "ep-1",
      target_kind: "shot_keyframe",
      target_id: "shot-1",
      image_path: "D:/studio/kf/shot1.png",
      rows: 2,
      cols: 2,
    });
    expect(splitOk).toBe(true);
    expect(
      useStudioStore.getState().versionsByTarget[targetVersionKey("shot_keyframe", "shot-1")]
    ).toHaveLength(2);

    const maskOk = await store.applyMaskEdit({
      project_id: "proj-p2",
      episode_id: "ep-1",
      target_kind: "shot_keyframe",
      target_id: "shot-1",
      image_path: "D:/studio/kf/shot1.png",
      mask_data_base64: "data:image/png;base64,ABC",
      prompt: "silver visor",
    });
    expect(maskOk).toBe(true);
    expect(useStudioStore.getState().maskModalTarget).toBeNull();

    // Extract Last Frame -> Next Keyframe
    const frameOk = await store.extractLastFrame({
      source_shot_id: "shot-1",
      target_shot_id: "shot-2",
    });
    expect(frameOk).toBe(true);
    expect(spy).toHaveBeenCalledWith(Actions.STUDIO_EXTRACT_LAST_FRAME, {
      episode_id: "ep-1",
      source_shot_id: "shot-1",
      target_shot_id: "shot-2",
    });

    // QC Review
    const report = await store.runQCReview({
      project_id: "proj-p2",
      episode_id: "ep-1",
      target_kind: "shot_video",
      target_id: "shot-1",
      media_path: "D:/studio/vid/shot1.mp4",
    });
    expect(report?.verdict).toBe("pass");
    expect(
      useStudioStore.getState().qcByTarget[targetVersionKey("shot_video", "shot-1")]?.overall_score
    ).toBe(9.2);

    // Character Voice Binding persistence
    const voiceSaved = await store.upsertAsset({
      id: characterAsset.id,
      project_id: "proj-p2",
      kind: "character",
      name: "Kai",
      voice_provider: "elevenlabs",
      voice_id: "rachel-pro",
      voice_style: "tense",
    });
    expect(voiceSaved).toBe(true);
    expect(useStudioStore.getState().episodeDetail?.assets[0].voice_id).toBe("rachel-pro");
  });

  it("renders ArcReel Phase 2 UI surfaces, voice binding & approval banners", () => {
    useStudioStore.setState({
      jobs: [sampleJob],
      costSummary: sampleCostSummary,
      queueDrawerOpen: true,
      pendingPreflight: {
        mode: "stage",
        stage: "generate_videos",
        opts: {},
        preflight: samplePreflight,
      },
      versionDrawerTarget: {
        projectId: "proj-p2",
        episodeId: "ep-1",
        targetKind: "shot_keyframe",
        targetId: "shot-1",
        label: "Shot 1",
        mediaPath: "D:/studio/kf/shot1.png",
      },
      gridModalTarget: {
        projectId: "proj-p2",
        episodeId: "ep-1",
        targetKind: "asset",
        targetId: "asset-char-1",
        imagePath: "D:/studio/assets/kai.png",
      },
      maskModalTarget: {
        projectId: "proj-p2",
        episodeId: "ep-1",
        targetKind: "shot_keyframe",
        targetId: "shot-1",
        imagePath: "D:/studio/kf/shot1.png",
        defaultPrompt: "cybernetic helmet",
      },
    });
    useAgentStore.setState({
      approvals: [
        {
          id: "ap-studio-1",
          provider_id: "codex",
          tool: "studio_queue_enqueue",
          title: "Approve Video Batch",
          summary: "Queue 2 shots (~$0.72)",
          status: "pending",
          created_at: "2026-11-17T14:00:00Z",
        },
      ],
    });

    // Video stage + pending approval banner
    useStudioStore.setState({ activeStage: "video" });
    const videoHtml = renderToStaticMarkup(React.createElement(StudioView));
    expect(videoHtml).toContain("Queue &amp; Cost");
    expect(videoHtml).toContain("Pending Studio Approvals");
    expect(videoHtml).toContain("Queue 2 shots (~$0.72)");
    expect(videoHtml).toContain("Last Frame → Next Keyframe");
    expect(videoHtml).toContain("Queue Lip-Sync");

    // Assets stage renders Character Voice Binding + Split Grid + Mask Edit
    useStudioStore.setState({ activeStage: "assets" });
    const assetsHtml = renderToStaticMarkup(React.createElement(StudioView));
    expect(assetsHtml).toContain("Character Voice Binding");
    expect(assetsHtml).toContain("alloy");
    expect(assetsHtml).toContain("Split Grid");
    expect(assetsHtml).toContain("Mask Edit");

    // Standalone modal renders
    const queueHtml = renderToStaticMarkup(React.createElement(StudioQueueCostDrawer));
    expect(queueHtml).toContain("Durable Queue &amp; Cost Ledger");
    expect(queueHtml).toContain("Approve Video Batch");
    expect(queueHtml).toContain("Actual Spend");

    const preflightHtml = renderToStaticMarkup(React.createElement(StudioPreflightDialog));
    expect(preflightHtml).toContain("Preflight &amp; Batch Approval Gate");
    expect(preflightHtml).toContain("$0.7200");

    const verHtml = renderToStaticMarkup(React.createElement(StudioVersionDrawer));
    expect(verHtml).toContain("Versions &amp; Visual QC");

    const gridHtml = renderToStaticMarkup(React.createElement(StudioGridSplitterModal));
    expect(gridHtml).toContain("Interactive Grid Splitter");

    const maskHtml = renderToStaticMarkup(React.createElement(StudioMaskEditorModal));
    expect(maskHtml).toContain("Mask &amp; Inpaint Editor");
    expect(maskHtml).toContain("cybernetic helmet");
  });
});
