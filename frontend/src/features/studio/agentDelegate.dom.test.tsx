// @vitest-environment jsdom
import { fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Actions,
  type ProviderDefaultsResult,
  type StudioEpisodeDetailResult,
} from "../../contracts/generated";
import { rtClient } from "../../lib/rtClient";
import { delegateStudioStageToAgent, isTextProviderConfigured } from "../agent/screenContext";
import { useAgentDrawerStore } from "../agent/useAgentDrawerStore";
import { useAgentStore } from "../agent/useAgentStore";
import { useProvidersStore } from "../providers/useProvidersStore";
import { AssetsStagePanel } from "./AssetsStagePanel";
import { ScriptStagePanel } from "./ScriptStagePanel";
import { StoryboardStagePanel } from "./StoryboardStagePanel";
import { useStudioStore } from "./useStudioStore";

const baseDetail: StudioEpisodeDetailResult = {
  project: {
    id: "proj-delegate-1",
    slug: "delegate-noir",
    title: "Delegate Noir",
    genre: "narration",
    production_mode: "storyboard_i2v",
    aspect_ratio: "16:9",
    style_prompt: "Rainy noir",
    root_dir: "C:/workspace/projects/delegate-noir",
    auto_approve_batches: false,
    default_text_model: "",
    default_image_model: "",
    default_video_model: "",
    default_tts_model: "",
    created_at: 1,
    updated_at: 1,
  },
  episode: {
    id: "ep-delegate-1",
    project_id: "proj-delegate-1",
    code: "ep01",
    title: "Episode 1",
    stage: "script",
    raw_script_path: "",
    script_json_path: "",
    assets_json_path: "",
    shots_json_path: "",
    timeline_json_path: "",
    created_at: 1,
    updated_at: 1,
  },
  raw_script: "Minh enters the dark station under neon rain.",
  assets: [],
  shots: [],
};

const emptyTextDefaults: ProviderDefaultsResult = {
  purposes: [
    {
      purpose: "text",
      capability: "llm",
      chosen: "",
      effective: "",
      chosen_unavailable: false,
      candidates: [],
    },
  ],
};

const readyTextDefaults: ProviderDefaultsResult = {
  purposes: [
    {
      purpose: "text",
      capability: "llm",
      chosen: "openai/gpt-4o",
      effective: "openai/gpt-4o",
      chosen_unavailable: false,
      candidates: [
        {
          ref: "openai/gpt-4o",
          provider_id: "openai",
          provider_name: "OpenAI",
          custom: false,
          is_provider_default: true,
          model: {
            model_id: "gpt-4o",
            display_name: "GPT-4o",
            capability: "llm",
            endpoint: "openai-chat",
            enabled: true,
            is_default: true,
            user_added: false,
            facts: {
              durations: [],
              resolutions: [],
              audio: false,
              max_output_tokens: 16384,
            },
          },
        },
      ],
    },
  ],
};

describe("Studio UI Agent fallback delegation when PurposeText is absent", () => {
  beforeEach(() => {
    useAgentDrawerStore.setState({
      panelOpen: false,
      activeTab: "connections",
      chatView: "history",
    });
    useAgentStore.setState({ prompt: "", sending: false });
    useProvidersStore.setState({ defaults: emptyTextDefaults });
    useStudioStore.setState({ saving: false, runningStage: null, episodeDetail: baseDetail });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("evaluates isTextProviderConfigured accurately from PurposeDefaults and model overrides", () => {
    expect(isTextProviderConfigured(null, "")).toBe(false);
    expect(isTextProviderConfigured(emptyTextDefaults, "")).toBe(false);
    expect(isTextProviderConfigured(emptyTextDefaults, "openai/gpt-4o")).toBe(true);
    expect(isTextProviderConfigured(readyTextDefaults, "")).toBe(true);
  });

  it("pre-fills prompt in Agent Drawer chat tab without auto-sending", () => {
    const reqSpy = vi.spyOn(rtClient, "request");

    delegateStudioStageToAgent({
      projectId: "proj-1",
      episodeId: "ep-1",
      stage: "script_normalize",
      prompt: "Pre-filled prompt for user check",
    });

    const drawer = useAgentDrawerStore.getState();
    expect(drawer.panelOpen).toBe(true);
    expect(drawer.activeTab).toBe("chat");
    expect(drawer.chatView).toBe("chat");
    expect(useAgentStore.getState().prompt).toBe("Pre-filled prompt for user check");
    expect(reqSpy).not.toHaveBeenCalled();
  });

  it("delegates Normalize, Extract Assets, and Author Shots to Agent Drawer when no PurposeText provider or model override exists", async () => {
    const runStageSpy = vi.fn().mockResolvedValue(true);
    const saveScriptSpy = vi.fn().mockResolvedValue(true);
    useStudioStore.setState({ runStage: runStageSpy, saveScript: saveScriptSpy });

    // 1. ScriptStagePanel: Normalize with modified raw script saves draft first then pre-fills Agent prompt.
    const scriptUI = render(<ScriptStagePanel detail={baseDetail} modelOverride="" />);
    const textarea = scriptUI.getByRole("textbox");
    fireEvent.change(textarea, { target: { value: "Edited raw script text." } });
    const normalizeBtn = scriptUI.getByRole("button", { name: /normalize/i });
    fireEvent.click(normalizeBtn);

    await waitFor(() => {
      expect(saveScriptSpy).toHaveBeenCalledWith("Edited raw script text.");
      expect(useAgentDrawerStore.getState().panelOpen).toBe(true);
    });
    expect(useAgentDrawerStore.getState().activeTab).toBe("chat");
    expect(useAgentStore.getState().prompt).toContain("ep-delegate-1");
    expect(runStageSpy).not.toHaveBeenCalled();
    scriptUI.unmount();

    // Reset drawer/prompt
    useAgentDrawerStore.setState({ panelOpen: false, activeTab: "connections" });
    useAgentStore.setState({ prompt: "" });

    // 2. AssetsStagePanel: Extract Assets is enabled with raw_script and pre-fills Agent prompt.
    const assetsUI = render(<AssetsStagePanel detail={baseDetail} textModel="" imageModel="" />);
    const extractBtn = assetsUI.getByRole("button", { name: /extract from script/i });
    expect((extractBtn as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(extractBtn);

    expect(useAgentDrawerStore.getState().panelOpen).toBe(true);
    expect(useAgentDrawerStore.getState().activeTab).toBe("chat");
    expect(useAgentStore.getState().prompt).toContain("studio_asset_upsert");
    expect(runStageSpy).not.toHaveBeenCalled();
    assetsUI.unmount();

    // Reset drawer/prompt
    useAgentDrawerStore.setState({ panelOpen: false, activeTab: "connections" });
    useAgentStore.setState({ prompt: "" });

    // 3. StoryboardStagePanel: Author Shot Prompts pre-fills Agent prompt without calling runStage.
    const storyboardUI = render(
      <StoryboardStagePanel detail={baseDetail} textModel="" imageModel="" />
    );
    const authorBtn = storyboardUI.getByRole("button", { name: /author shot prompts/i });
    expect((authorBtn as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(authorBtn);

    expect(useAgentDrawerStore.getState().panelOpen).toBe(true);
    expect(useAgentDrawerStore.getState().activeTab).toBe("chat");
    expect(useAgentStore.getState().prompt).toContain("studio_shots_batch_upsert");
    expect(runStageSpy).not.toHaveBeenCalled();
    expect(Actions.STUDIO_EPISODE_RUN_STAGE).toBe("studio:episode_run_stage");
    storyboardUI.unmount();
  });

  it("calls runStage directly when PurposeText provider candidate exists", async () => {
    useProvidersStore.setState({ defaults: readyTextDefaults });
    const runStageSpy = vi.fn().mockResolvedValue(true);
    useStudioStore.setState({ runStage: runStageSpy });

    const scriptUI = render(<ScriptStagePanel detail={baseDetail} modelOverride="" />);
    fireEvent.click(scriptUI.getByRole("button", { name: /normalize/i }));

    await waitFor(() => {
      expect(runStageSpy).toHaveBeenCalledWith("script_normalize", {});
    });
    expect(useAgentDrawerStore.getState().panelOpen).toBe(false);
    scriptUI.unmount();
  });
});
