import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { navRoutes } from "../../app/navigation";
import {
  Actions,
  NodeTypes,
  type ProbeMediaOutput,
  type StudioEpisodeDetailResult,
  type StudioProject,
} from "../../contracts/generated";
import { rtClient } from "../../lib/rtClient";
import { useNodeSpecsStore } from "../workflow/useNodeSpecs";
import { StudioView } from "./StudioView";
import { useStudioStore } from "./useStudioStore";

vi.mock("react-router-dom", async (importOriginal) => {
  const mod = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...mod,
    useNavigate: () => vi.fn(),
  };
});

const sampleProject: StudioProject = {
  id: "proj-1",
  slug: "neon-courier",
  title: "Neon Courier",
  genre: "drama",
  production_mode: "storyboard_i2v",
  aspect_ratio: "16:9",
  style_prompt: "Neo-noir anime",
  root_dir: "C:/workspace/projects/neon-courier",
  auto_approve_batches: false,
  default_text_model: "",
  default_image_model: "",
  default_video_model: "",
  default_tts_model: "",
  created_at: 1,
  updated_at: 2,
};

const sampleEpisodeDetail: StudioEpisodeDetailResult = {
  project: sampleProject,
  episode: {
    id: "ep-1",
    project_id: "proj-1",
    code: "ep01",
    title: "Episode 1",
    stage: "script",
    raw_script_path: "C:/workspace/projects/neon-courier/episodes/ep01/script/raw_screenplay.txt",
    script_json_path: "",
    assets_json_path: "",
    shots_json_path: "",
    timeline_json_path: "",
    created_at: 1,
    updated_at: 2,
  },
  assets: [
    {
      id: "asset-1",
      project_id: "proj-1",
      kind: "character",
      name: "Minh",
      code_tag: "@Minh",
      description: "Courier in dark coat",
      prompt: "3-view turnaround of Minh",
      primary_asset_id: "",
      primary_path: "",
      updated_at: 2,
    },
  ],
  shots: [
    {
      id: "shot-1",
      episode_id: "ep-1",
      seq: 1,
      scene_code: "S01",
      title: "Rainy Arrival",
      shot_mode: "single",
      duration_sec: 5,
      image_prompt: "Wide shot of @Minh in rain",
      video_prompt: "Slow push in",
      narration_text: "Midnight rain fell over District 4.",
      ref_asset_tags: ["@Minh"],
      keyframe_asset_id: "",
      keyframe_path: "",
      video_asset_id: "",
      video_path: "C:/workspace/projects/neon-courier/episodes/ep01/media/videos/shot1.mp4",
      audio_asset_id: "",
      audio_path: "",
      status: "video_ready",
      error_text: "",
      updated_at: 2,
    },
  ],
  raw_script: "Minh bước vào quán cà phê giữa đêm mưa.",
};

describe("/studio feature MVP", () => {
  beforeEach(() => {
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
      getSnapshot()
    );
    useStudioStore.setState({
      projects: [],
      projectsDir: "",
      projectDetail: null,
      episodeDetail: null,
      activeProjectId: null,
      activeEpisodeId: null,
      activeStage: "script",
      selectedAssetIds: [],
      loadingProjects: false,
      loadingEpisode: false,
      saving: false,
      runningStage: null,
      actionProgress: null,
      batchProgress: null,
      probeMap: {},
      error: null,
      lastSummary: "",
    });

    useNodeSpecsStore.setState({
      specs: [
        {
          type: NodeTypes.SCRIPT_NORMALIZE,
          provider: "tool",
          label: "Screenplay Normalizer",
          description: "Normalize screenplay",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "aspect_ratio",
              label: "Aspect Ratio",
              kind: "select",
              default: "16:9",
              options: ["16:9", "9:16"],
            },
          ],
        },
        {
          type: NodeTypes.AUTHOR_SHOT_PROMPTS,
          provider: "tool",
          label: "Shot Prompt Author",
          description: "Author shot prompts",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "shot_mode_default",
              label: "Shot Mode",
              kind: "select",
              default: "single",
              options: ["single", "r2v"],
            },
          ],
        },
        {
          type: NodeTypes.VIDEO_GENERATE,
          provider: "tool",
          label: "Video Generator",
          description: "Generate video",
          inputs: [],
          outputs: [],
          params: [
            {
              name: "resolution",
              label: "Resolution",
              kind: "select",
              default: "720p",
              options: ["720p", "1080p"],
            },
          ],
        },
        {
          type: NodeTypes.VIDEO_CONCAT_BASIC,
          provider: "tool",
          label: "Basic Video Assembler",
          description: "Concat video",
          inputs: [],
          outputs: [],
          params: [
            { name: "resolution", label: "Resolution", kind: "text", default: "1920x1080" },
            { name: "fps", label: "FPS", kind: "integer", default: "24" },
          ],
        },
      ],
      loading: false,
      error: null,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("registers /studio in navRoutes while preserving existing feature routes", () => {
    const paths = navRoutes.map((r) => r.path);
    expect(paths).toContain("/");
    expect(paths).toContain("/studio");
    expect(paths).toContain("/watermark");
    expect(paths).toContain("/image");
    expect(paths).toContain("/video");
    expect(paths).toContain("/tts");
  });

  it("creates projects, saves scripts, runs stages, and probes clips via studio store", async () => {
    const spy = vi.spyOn(rtClient, "request").mockImplementation((async (
      action: string,
      payload: unknown
    ) => {
      if (action === Actions.STUDIO_PROJECT_CREATE) {
        return {
          project: sampleProject,
          episodes: [sampleEpisodeDetail.episode],
          assets: sampleEpisodeDetail.assets,
          active_episode: sampleEpisodeDetail,
        };
      }
      if (action === Actions.STUDIO_EPISODE_SAVE_SCRIPT) {
        return {
          ...sampleEpisodeDetail,
          raw_script: (payload as { raw_script?: string }).raw_script ?? "",
        };
      }
      if (action === Actions.STUDIO_EPISODE_RUN_STAGE) {
        return {
          episode: sampleEpisodeDetail,
          completed_count: 1,
          failed_count: 0,
          summary: "Normalized 1 scene / 1 beat",
        };
      }
      if (action === Actions.WORKFLOW_RUN_NODE) {
        const probe: ProbeMediaOutput = {
          probe_json: "{}",
          duration_sec: 5.0,
          width: 1280,
          height: 720,
          fps: 24,
          has_audio: true,
          silent_ratio: 0.1,
          video_codec: "h264",
          audio_codec: "aac",
          format_name: "mp4",
        };
        return { output: probe };
      }
      return {};
    }) as typeof rtClient.request);

    const store = useStudioStore.getState();
    const ok = await store.createProject({ title: "Neon Courier" });
    expect(ok).toBe(true);
    expect(useStudioStore.getState().activeProjectId).toBe("proj-1");
    expect(useStudioStore.getState().activeEpisodeId).toBe("ep-1");

    await useStudioStore.getState().saveScript("Updated screenplay text");
    expect(useStudioStore.getState().episodeDetail?.raw_script).toBe("Updated screenplay text");

    await useStudioStore.getState().runStage("script_normalize");
    expect(useStudioStore.getState().lastSummary).toContain("Normalized");

    const probed = await useStudioStore.getState().probeShot(sampleEpisodeDetail.shots[0]);
    expect(probed?.duration_sec).toBe(5.0);
    expect(useStudioStore.getState().probeMap["shot-1"]?.fps).toBe(24);
    expect(spy).toHaveBeenCalled();
  });

  it("renders ArcReel 5-stage StudioView without errors", () => {
    useStudioStore.setState({
      projects: [sampleProject],
      projectDetail: {
        project: sampleProject,
        episodes: [sampleEpisodeDetail.episode],
        assets: sampleEpisodeDetail.assets,
        active_episode: sampleEpisodeDetail,
      },
      episodeDetail: sampleEpisodeDetail,
      activeProjectId: "proj-1",
      activeEpisodeId: "ep-1",
      activeStage: "script",
    });

    for (const stage of ["script", "assets", "storyboard", "video", "export"] as const) {
      useStudioStore.setState({ activeStage: stage });
      const html = renderToStaticMarkup(React.createElement(StudioView));
      expect(html).toContain("Neon Courier");
      expect(html).toContain("ep01");
    }
  });
});
