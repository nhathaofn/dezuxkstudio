import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { type BinaryInfo, NodeTypes, type Spec, type SystemInfo } from "../../contracts/generated";
import { ActionError } from "../../lib/rtClient";
import { BinaryCard } from "../binaries/BinaryCard";
import { HealthBadge } from "../providers/HealthBadge";
import { SecretInput } from "../providers/SecretInput";
import { SystemCard } from "../system/SystemCard";
import { useSystemStore } from "../system/useSystemStore";
import { useNodeSpecsStore } from "../workflow/useNodeSpecs";
import { ImageGenView } from "./ImageGenView";
import { MediaResultCard } from "./MediaResultCard";
import { VideoGenView } from "./VideoGenView";
import { useImageGenStore } from "./useMediaGenStores";

vi.mock("react-router-dom", async (importOriginal) => {
  const mod = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...mod,
    useNavigate: () => vi.fn(),
  };
});

describe("Phase 1 Wave B - Feature Worker 4 Enhancements", () => {
  describe("Task 1: Provider Probe Latency & Masked Secret", () => {
    it("HealthBadge renders latencyMs when provided", () => {
      const html = renderToStaticMarkup(
        React.createElement(HealthBadge, {
          health: {
            tested: true,
            outcome: "ok",
            detail: "",
            tested_at: "2026-11-15T12:00:00Z",
            dead: false,
          },
          latencyMs: 42,
        })
      );
      expect(html).toContain("42ms");
      expect(html).toContain("providers-latency");
    });

    it("SecretInput masks input as password and disables visibility toggle when empty", () => {
      const html = renderToStaticMarkup(
        React.createElement(SecretInput, {
          label: "API Key",
          value: "",
          hint: "sk-...abcd",
          onChange: () => {},
          onClear: () => {},
        })
      );
      expect(html).toContain('type="password"');
      expect(html.toLowerCase()).toContain('autocomplete="new-password"');
      expect(html).toContain("disabled");
      expect(html).toContain("sk-...abcd");
    });

    it("SecretInput disables input and shows danger alert when cleared", () => {
      const html = renderToStaticMarkup(
        React.createElement(SecretInput, {
          label: "API Key",
          value: "",
          hint: "sk-...abcd",
          cleared: true,
          onChange: () => {},
          onClear: () => {},
        })
      );
      expect(html).toContain("disabled");
      expect(html).toContain("providers-danger");
    });
  });

  describe("Task 2: Mode Gate (Desktop vs Remote Server/Client)", () => {
    it("SystemCard disables folder open buttons in remote server mode", () => {
      useSystemStore.setState({
        info: {
          mode: "server",
          build: {
            version: "1.0.0",
            commit: "abc",
            os: "windows",
            arch: "amd64",
            go_version: "1.24",
          },
          runtime: { uptime_seconds: 3600, goroutines: 10, memory_alloc_bytes: 1000 },
          paths: {
            config_dir: "C:/app",
            data_dir: "C:/app",
            logs_dir: "C:/app",
            tools_dir: "C:/app",
          },
        } as unknown as SystemInfo,
      });

      const html = renderToStaticMarkup(React.createElement(SystemCard));
      expect(html).toContain("disabled");
      expect(html).toContain("This action is only available in desktop mode");
    });

    it("BinaryCard disables browse button in remote client mode with informative tooltip", () => {
      useSystemStore.setState({
        info: {
          mode: "client",
          build: {
            version: "1.0.0",
            commit: "abc",
            os: "windows",
            arch: "amd64",
            go_version: "1.24",
          },
          runtime: { uptime_seconds: 3600, goroutines: 10, memory_alloc_bytes: 1000 },
          paths: {
            config_dir: "C:/app",
            data_dir: "C:/app",
            logs_dir: "C:/app",
            tools_dir: "C:/app",
          },
        } as unknown as SystemInfo,
      });

      const dummyBinary: BinaryInfo = {
        id: "ffmpeg",
        name: "FFmpeg",
        description: "Media processor",
        executable: "ffmpeg.exe",
        resolved_path: "C:/app/tools/ffmpeg.exe",
        custom_path: "",
        version: "7.0",
        status: "available",
        source: "tools",
        can_download: true,
      };

      const html = renderToStaticMarkup(React.createElement(BinaryCard, { binary: dummyBinary }));
      expect(html).toContain("disabled");
      expect(html).toContain("This action is only available in desktop mode");
    });
  });

  describe("Task 3: Binaries UX Progress & Warnings", () => {
    it("BinaryCard displays inline warning if custom binary path is invalid or empty", () => {
      const invalidBinary: BinaryInfo = {
        id: "ffmpeg",
        name: "FFmpeg",
        description: "Media processor",
        executable: "ffmpeg.exe",
        resolved_path: "",
        custom_path: "C:/invalid/path/ffmpeg.exe",
        version: "",
        status: "invalid",
        source: "custom",
        can_download: true,
      };

      const html = renderToStaticMarkup(React.createElement(BinaryCard, { binary: invalidBinary }));
      expect(html).toContain("binary-path-warning");
      expect(html).toContain("configured custom executable path is invalid");
    });
  });

  describe("Task 4: MediaGen UX Polish", () => {
    it("ImageGenView renders quick aspect ratio presets (1:1, 16:9, 9:16, 4:3) from node spec", () => {
      const spy = vi
        .spyOn(React, "useSyncExternalStore")
        .mockImplementation((_sub, getSnap) => getSnap());
      try {
        const specs: Spec[] = [
          {
            type: NodeTypes.IMAGE_GENERATE,
            provider: "mediagen",
            label: "Image",
            description: "",
            inputs: [],
            outputs: [],
            params: [
              {
                name: "aspect_ratio",
                label: "Aspect",
                kind: "enum",
                default: "1:1",
                options: ["1:1", "16:9", "9:16", "4:3"],
              },
            ],
          },
        ];
        useNodeSpecsStore.setState({ specs });
        const html = renderToStaticMarkup(React.createElement(ImageGenView));
        expect(html).toContain("mediagen-aspect-presets");
        expect(html).toContain(">1:1<");
        expect(html).toContain(">16:9<");
        expect(html).toContain(">9:16<");
        expect(html).toContain(">4:3<");
      } finally {
        spy.mockRestore();
      }
    });

    it("VideoGenView renders quick aspect ratio presets (1:1, 16:9, 9:16, 4:3) from node spec", () => {
      const spy = vi
        .spyOn(React, "useSyncExternalStore")
        .mockImplementation((_sub, getSnap) => getSnap());
      try {
        const specs: Spec[] = [
          {
            type: NodeTypes.VIDEO_GENERATE,
            provider: "mediagen",
            label: "Video",
            description: "",
            inputs: [],
            outputs: [],
            params: [
              {
                name: "aspect_ratio",
                label: "Aspect",
                kind: "enum",
                default: "16:9",
                options: ["16:9", "9:16", "1:1", "4:3"],
              },
            ],
          },
        ];
        useNodeSpecsStore.setState({ specs });
        const html = renderToStaticMarkup(React.createElement(VideoGenView));
        expect(html).toContain("mediagen-aspect-presets");
        expect(html).toContain(">1:1<");
        expect(html).toContain(">16:9<");
        expect(html).toContain(">9:16<");
        expect(html).toContain(">4:3<");
      } finally {
        spy.mockRestore();
      }
    });

    it("MediaResultCard renders retry and dismiss buttons when error exists", () => {
      const onRetry = vi.fn();
      const onDismiss = vi.fn();

      const html = renderToStaticMarkup(
        React.createElement(MediaResultCard, {
          kind: "image",
          loading: false,
          progress: null,
          error: new ActionError("ERR_UNAVAILABLE", "GPU pipeline timeout"),
          result: null,
          downloadingPath: null,
          onDownload: () => {},
          onRetry: onRetry,
          onDismissError: onDismiss,
        })
      );

      expect(html).toContain("mediagen-error-box");
      expect(html).toContain("GPU pipeline timeout");
      expect(html).toContain("Retry");
      expect(html).toContain("Dismiss error");
    });

    it("MediaResultCard renders AudioWaveformPlayer for TTS result with audio preview", () => {
      const html = renderToStaticMarkup(
        React.createElement(MediaResultCard, {
          kind: "tts",
          loading: false,
          progress: null,
          error: null,
          result: {
            path: "C:/output/speech.wav",
            mimeType: "audio/wav",
            provider: "openai",
            model: "tts-1",
            sizeBytes: 10240,
            duration: 6,
          },
          downloadingPath: null,
          onDownload: () => {},
        })
      );

      expect(html).toContain("watermark-results-body");
    });

    it("useMediaGenStores clearError preserves inputs intact for retry", () => {
      const imgStore = useImageGenStore.getState();
      imgStore.setPrompt("A cybernetic landscape");
      imgStore.setAspectRatio("16:9");
      imgStore.setResolution("4K");
      imgStore.setSeed("42");

      useImageGenStore.setState({ error: new ActionError("ERR_VALIDATION", "Transient failure") });
      expect(useImageGenStore.getState().error).not.toBeNull();

      useImageGenStore.getState().clearError();
      expect(useImageGenStore.getState().error).toBeNull();
      expect(useImageGenStore.getState().prompt).toBe("A cybernetic landscape");
      expect(useImageGenStore.getState().aspectRatio).toBe("16:9");
      expect(useImageGenStore.getState().resolution).toBe("4K");
      expect(useImageGenStore.getState().seed).toBe("42");
    });
  });
});
