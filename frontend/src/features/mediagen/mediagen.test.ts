import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { navRoutes } from "../../app/navigation";
import {
  Actions,
  NodeTypes,
  type ProviderDefaultsResult,
  type Spec,
} from "../../contracts/generated";
import { rtClient, uploadMediaFile } from "../../lib/rtClient";
import { usePreferencesStore } from "../preferences/usePreferencesStore";
import { useProvidersStore } from "../providers/useProvidersStore";
import { useNodeSpecsStore } from "../workflow/useNodeSpecs";
import { ImageGenView } from "./ImageGenView";
import { TTSGenView } from "./TTSGenView";
import { VideoGenView } from "./VideoGenView";
import { fromBrowserFile, fromLocalPath } from "./types";
import { useImageGenStore, useTTSGenStore, useVideoGenStore } from "./useMediaGenStores";

vi.mock("react-router-dom", async (importOriginal) => {
  const mod = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...mod,
    useNavigate: () => vi.fn(),
  };
});

vi.mock("../../lib/rtClient", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/rtClient")>();
  return {
    ...mod,
    uploadMediaFile: vi.fn(async (file: File) => ({
      path: `C:/staged/${file.name}`,
      name: file.name,
      size: file.size,
    })),
    saveMediaToDevice: vi.fn(async () => {}),
  };
});

const imageSpec: Spec = {
  type: NodeTypes.IMAGE_GENERATE,
  provider: "mediagen",
  label: "Generate Image",
  description: "Image generation",
  inputs: [],
  outputs: [],
  params: [
    {
      name: "aspect_ratio",
      label: "Aspect Ratio",
      kind: "enum",
      default: "1:1",
      options: ["1:1", "16:9", "9:16", "4:3"],
    },
    {
      name: "resolution",
      label: "Resolution",
      kind: "enum",
      default: "1K",
      options: ["1K", "2K", "4K"],
    },
  ],
};

const videoSpec: Spec = {
  type: NodeTypes.VIDEO_GENERATE,
  provider: "mediagen",
  label: "Generate Video",
  description: "Video generation",
  inputs: [],
  outputs: [],
  params: [
    {
      name: "aspect_ratio",
      label: "Aspect Ratio",
      kind: "enum",
      default: "16:9",
      options: ["16:9", "9:16", "1:1"],
    },
    {
      name: "resolution",
      label: "Resolution",
      kind: "enum",
      default: "720p",
      options: ["480p", "720p", "1080p"],
    },
  ],
};

const ttsSpec: Spec = {
  type: NodeTypes.TTS_GENERATE,
  provider: "mediagen",
  label: "Text to Speech",
  description: "TTS generation",
  inputs: [],
  outputs: [],
  params: [
    {
      name: "format",
      label: "Format",
      kind: "enum",
      default: "mp3",
      options: ["mp3", "wav", "opus"],
    },
  ],
};

const sampleDefaults: ProviderDefaultsResult = {
  purposes: [
    {
      purpose: "image",
      capability: "image",
      chosen: "",
      effective: "openai/gpt-image-1",
      chosen_unavailable: false,
      candidates: [
        {
          ref: "openai/gpt-image-1",
          provider_id: "openai",
          provider_name: "OpenAI",
          custom: false,
          is_provider_default: true,
          model: {
            model_id: "gpt-image-1",
            display_name: "GPT Image 1",
            capability: "image",
            endpoint: "openai-images",
            enabled: true,
            is_default: true,
            user_added: false,
            facts: { durations: [], resolutions: [], audio: false, max_output_tokens: 0 },
          },
        },
      ],
    },
    {
      purpose: "video",
      capability: "video",
      chosen: "",
      effective: "google/veo-3",
      chosen_unavailable: false,
      candidates: [
        {
          ref: "google/veo-3",
          provider_id: "google",
          provider_name: "Google Gemini",
          custom: false,
          is_provider_default: true,
          model: {
            model_id: "veo-3",
            display_name: "Veo 3",
            capability: "video",
            endpoint: "gemini-video",
            enabled: true,
            is_default: true,
            user_added: false,
            facts: {
              durations: [4, 8],
              resolutions: ["720p", "1080p"],
              audio: true,
              max_output_tokens: 0,
            },
          },
        },
      ],
    },
    {
      purpose: "tts",
      capability: "tts",
      chosen: "",
      effective: "openai/gpt-4o-mini-tts",
      chosen_unavailable: false,
      candidates: [
        {
          ref: "openai/gpt-4o-mini-tts",
          provider_id: "openai",
          provider_name: "OpenAI",
          custom: false,
          is_provider_default: true,
          model: {
            model_id: "gpt-4o-mini-tts",
            display_name: "GPT-4o Mini TTS",
            capability: "tts",
            endpoint: "openai-tts",
            enabled: true,
            is_default: true,
            user_added: false,
            facts: { durations: [], resolutions: [], audio: false, max_output_tokens: 0 },
          },
        },
      ],
    },
  ],
};

vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
  getSnapshot()
);

function renderView(Component: React.FC): string {
  return renderToStaticMarkup(React.createElement(Component));
}

describe("MediaGen navigation & UI", () => {
  const initialProvidersState = useProvidersStore.getState();

  beforeEach(() => {
    usePreferencesStore.setState({ prefs: { theme: "light", language: "en" } });
    useNodeSpecsStore.setState({
      specs: [imageSpec, videoSpec, ttsSpec],
      loading: false,
      error: null,
    });
    useProvidersStore.setState({
      ...initialProvidersState,
      defaults: sampleDefaults,
    });
    useImageGenStore.getState().reset();
    useImageGenStore.setState({
      model: "",
      aspectRatio: null,
      resolution: null,
      seed: "",
      outputDir: "",
    });
    useVideoGenStore.getState().reset();
    useVideoGenStore.setState({
      model: "",
      aspectRatio: null,
      resolution: null,
      duration: null,
      generateAudio: false,
      seed: "",
      outputDir: "",
    });
    useTTSGenStore.getState().reset();
    useTTSGenStore.setState({
      model: "",
      voice: "",
      format: null,
      speed: "1.0",
      language: "",
      outputDir: "",
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, getSnapshot) =>
      getSnapshot()
    );
  });

  it("registers Image, Video, and TTS tabs directly after Watermark", () => {
    const paths = navRoutes.map((r) => r.path);
    const wmIdx = paths.indexOf("/watermark");
    expect(wmIdx).toBeGreaterThanOrEqual(0);
    expect(paths.slice(wmIdx, wmIdx + 4)).toEqual(["/watermark", "/image", "/video", "/tts"]);
  });

  it("renders ImageGenView with provider candidates, progress, and output metadata", () => {
    const htmlIdle = renderView(ImageGenView);
    expect(htmlIdle).toContain("Image Generation");
    expect(htmlIdle).toContain("Auto (→ OpenAI · GPT Image 1)");
    expect(htmlIdle).toContain("Ready to generate");

    useImageGenStore.setState({
      prompt: "neon alleyway",
      refs: [fromLocalPath("C:/ref.png")],
      loading: true,
      progress: {
        percent: 45,
        message: "Requesting...",
        msg_key: "mediagen.progress.generating_image",
      },
    });
    const htmlLoading = renderView(ImageGenView);
    expect(htmlLoading).toContain("Requesting image generation...");
    expect(htmlLoading).toContain('role="progressbar"');
    expect(htmlLoading).toContain("1 refs");

    useImageGenStore.setState({
      loading: false,
      progress: null,
      result: {
        image: { localUrl: "C:/out/img.png", kind: "image" },
        path: "C:/out/img.png",
        mimeType: "image/png",
        provider: "openai",
        model: "gpt-image-1",
        sizeBytes: 204800,
      },
    });
    const htmlDone = renderView(ImageGenView);
    expect(htmlDone).toContain("C:/out/img.png");
    expect(htmlDone).toContain("image/png");
    expect(htmlDone).toContain("Copy Path");
    expect(htmlDone).toContain("Download");
  });

  it("shows provider configuration warning when no provider candidates exist", () => {
    useProvidersStore.setState({
      ...initialProvidersState,
      defaults: {
        purposes: [
          {
            purpose: "image",
            capability: "image",
            chosen: "",
            effective: "",
            chosen_unavailable: false,
            candidates: [],
          },
        ],
      },
    });
    const html = renderView(ImageGenView);
    expect(html).toContain("No provider configured for this purpose.");
    expect(html).toContain("Configure a provider");
  });

  it("renders VideoGenView using model facts for duration/resolution and polling progress", () => {
    const html = renderView(VideoGenView);
    expect(html).toContain("Video Generation");
    expect(html).toContain("4s");
    expect(html).toContain("8s");
    expect(html).toContain("1080p");
    expect(html).not.toContain("480p");

    useVideoGenStore.setState({
      loading: true,
      progress: {
        percent: 60,
        message: "Polling...",
        msg_key: "mediagen.progress.polling_video",
        msg_params: { attempt: "3" },
      },
    });
    const htmlPolling = renderView(VideoGenView);
    expect(htmlPolling).toContain("Rendering video (poll 3)...");
    expect(htmlPolling).toContain("width:60%");

    useVideoGenStore.setState({
      loading: false,
      progress: null,
      result: {
        video: { localUrl: "C:/out/clip.mp4", kind: "video" },
        path: "C:/out/clip.mp4",
        mimeType: "video/mp4",
        provider: "google",
        model: "veo-3",
        duration: 8,
        sizeBytes: 4194304,
      },
    });
    const htmlDone = renderView(VideoGenView);
    expect(htmlDone).toContain("C:/out/clip.mp4");
    expect(htmlDone).toContain("8s");
  });

  it("renders TTSGenView fields and synthesized audio metadata", () => {
    useTTSGenStore.setState({
      text: "Hello world",
      voice: "alloy",
      format: "wav",
      speed: "1.25",
      language: "en",
      result: {
        audio: { localUrl: "C:/out/speech.wav", kind: "audio" },
        path: "C:/out/speech.wav",
        mimeType: "audio/wav",
        provider: "openai",
        model: "gpt-4o-mini-tts",
        sizeBytes: 98304,
      },
    });
    const html = renderView(TTSGenView);
    expect(html).toContain("Text to Speech");
    expect(html).toContain("C:/out/speech.wav");
    expect(html).toContain("audio/wav");
  });

  it("invokes WORKFLOW_RUN_NODE for image generation including browser File upload", async () => {
    const requestSpy = vi.spyOn(rtClient, "request").mockResolvedValueOnce({
      output: {
        image: { localUrl: "C:/out/gen.png", kind: "image" },
        path: "C:/out/gen.png",
        mimeType: "image/png",
        provider: "openai",
        model: "gpt-image-1",
        sizeBytes: 1024,
      },
    } as never);

    const file = new File(["png"], "sketch.png", { type: "image/png" });
    const store = useImageGenStore.getState();
    store.setPrompt("sunset skyline");
    store.addRefs([fromBrowserFile(file), fromLocalPath("C:/refs/style.png")]);
    store.setAspectRatio("16:9");
    store.setResolution("2K");
    store.setSeed("42");

    await useImageGenStore.getState().generate(imageSpec);

    expect(uploadMediaFile).toHaveBeenCalledTimes(1);
    expect(requestSpy).toHaveBeenCalledWith(
      Actions.WORKFLOW_RUN_NODE,
      {
        type: NodeTypes.IMAGE_GENERATE,
        input: {
          prompt: "sunset skyline",
          reference_images: [
            { localUrl: "C:/staged/sketch.png", kind: "image" },
            { localUrl: "C:/refs/style.png", kind: "image" },
          ],
          aspect_ratio: "16:9",
          resolution: "2K",
          seed: 42,
        },
      },
      expect.any(Object)
    );
    expect(useImageGenStore.getState().result?.path).toBe("C:/out/gen.png");
  });

  it("invokes WORKFLOW_RUN_NODE for video and TTS stores", async () => {
    const requestSpy = vi
      .spyOn(rtClient, "request")
      .mockResolvedValueOnce({
        output: {
          video: { localUrl: "C:/out/v.mp4", kind: "video" },
          path: "C:/out/v.mp4",
          mimeType: "video/mp4",
          provider: "google",
          model: "veo-3",
          duration: 8,
          sizeBytes: 5120,
        },
      } as never)
      .mockResolvedValueOnce({
        output: {
          audio: { localUrl: "C:/out/a.mp3", kind: "audio" },
          path: "C:/out/a.mp3",
          mimeType: "audio/mpeg",
          provider: "openai",
          model: "gpt-4o-mini-tts",
          sizeBytes: 2048,
        },
      } as never);

    const vStore = useVideoGenStore.getState();
    vStore.setPrompt("camera orbit");
    vStore.setStartImage(fromLocalPath("C:/start.png"));
    vStore.setEndImage(fromLocalPath("C:/end.png"));
    vStore.setDuration(8);
    vStore.setGenerateAudio(true);
    await useVideoGenStore.getState().generate(videoSpec);

    expect(requestSpy).toHaveBeenNthCalledWith(
      1,
      Actions.WORKFLOW_RUN_NODE,
      {
        type: NodeTypes.VIDEO_GENERATE,
        input: {
          prompt: "camera orbit",
          start_image: { localUrl: "C:/start.png", kind: "image" },
          end_image: { localUrl: "C:/end.png", kind: "image" },
          aspect_ratio: "16:9",
          resolution: "720p",
          duration: 8,
          generate_audio: true,
        },
      },
      expect.any(Object)
    );

    const tStore = useTTSGenStore.getState();
    tStore.setText("Narrate scene one");
    tStore.setVoice("Cherry");
    tStore.setFormat("mp3");
    tStore.setSpeed("1.25");
    tStore.setLanguage("en");
    await useTTSGenStore.getState().generate(ttsSpec);

    expect(requestSpy).toHaveBeenNthCalledWith(
      2,
      Actions.WORKFLOW_RUN_NODE,
      {
        type: NodeTypes.TTS_GENERATE,
        input: {
          text: "Narrate scene one",
          voice: "Cherry",
          format: "mp3",
          speed: 1.25,
          language: "en",
        },
      },
      expect.any(Object)
    );
  });
});
