import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MediaOutputNode, type MediaOutputItem } from "./MediaOutputNode";
import { ActionError } from "../../lib/rtClient";

describe("MediaOutputNode UI Component", () => {
  it("renders empty state cleanly when item is null and not loading", () => {
    const html = renderToStaticMarkup(<MediaOutputNode kind="image" item={null} loading={false} />);

    expect(html).toContain("empty-state");
    expect(html).toContain("Ready to generate");
  });

  it("renders loading state with progress percentage and spinner", () => {
    const html = renderToStaticMarkup(
      <MediaOutputNode
        kind="video"
        item={null}
        loading={true}
        progress={{ percent: 65, message: "Rendering scene frames..." }}
      />
    );

    expect(html).toContain("Rendering scene frames...");
    expect(html).toContain('style="width:65%"');
  });

  it("renders error banner with monochromatic icon-only retry and dismiss buttons", () => {
    const onRetry = vi.fn();
    const onDismiss = vi.fn();

    const html = renderToStaticMarkup(
      <MediaOutputNode
        kind="image"
        item={null}
        loading={false}
        error={new ActionError("ERR_TIMEOUT", "GPU request timed out")}
        onRetry={onRetry}
        onDismissError={onDismiss}
      />
    );

    expect(html).toContain("mediagen-error-box");
    expect(html).toContain("GPU request timed out");
    expect(html).toContain('title="Retry"');
    expect(html).toContain('aria-label="Dismiss error"');
  });

  it("renders prompt strip with hover tooltip and copy button when prompt is provided", () => {
    const item: MediaOutputItem = {
      outputPath: "C:/out/cyberpunk_city.png",
      prompt: "A neon-lit cyberpunk metropolis in rain, 8k resolution, cinematic lighting",
      mimeType: "image/png",
      provider: "fal-ai",
      model: "flux-realism",
      sizeBytes: 2048576,
    };

    const html = renderToStaticMarkup(<MediaOutputNode kind="image" item={item} loading={false} />);

    expect(html).toContain("media-output-prompt-strip");
    expect(html).toContain("A neon-lit cyberpunk metropolis in rain");
    expect(html).toContain('title="Copy Prompt"');
    expect(html).toContain("flux-realism");
    expect(html).toContain("2.0 MB");
  });

  it("renders image preview and Before/After compare button when sourcePath exists", () => {
    const item: MediaOutputItem = {
      outputPath: "C:/out/cleaned.png",
      outputUrl: "blob:http://localhost/out-123",
      sourcePath: "C:/in/original_with_wm.png",
      sourceUrl: "blob:http://localhost/in-123",
      mimeType: "image/png",
    };

    const html = renderToStaticMarkup(<MediaOutputNode kind="image" item={item} loading={false} />);

    expect(html).toContain("media-output-viewport");
    expect(html).toContain('src="blob:http://localhost/out-123"');
    expect(html).toContain('title="Compare Before / After"');
  });

  it("renders HTML5 video element for video kind", () => {
    const item: MediaOutputItem = {
      outputPath: "C:/out/generated_video.mp4",
      outputUrl: "blob:http://localhost/video-456",
      mimeType: "video/mp4",
      duration: 5.0,
      sizeBytes: 8388608,
    };

    const html = renderToStaticMarkup(<MediaOutputNode kind="video" item={item} loading={false} />);

    expect(html).toContain("<video");
    expect(html).toContain('src="blob:http://localhost/video-456"');
    expect(html).toContain("5.0s");
    expect(html).toContain("8.0 MB");
  });

  it("renders AudioWaveformPlayer for audio and tts kind with icon-only controls", () => {
    const item: MediaOutputItem = {
      outputPath: "C:/out/speech.wav",
      outputUrl: "blob:http://localhost/audio-789",
      mimeType: "audio/wav",
      duration: 7.5,
      provider: "openai",
      model: "tts-1",
      sizeBytes: 120000,
    };

    const html = renderToStaticMarkup(<MediaOutputNode kind="tts" item={item} loading={false} />);

    expect(html).toContain("mediagen-waveform-player");
    expect(html).toContain("mediagen-waveform-bars");
    expect(html).toContain('aria-label="Play"');
    expect(html).toContain('aria-label="Restart"');
    expect(html).toContain('aria-label="Mute"');
  });

  it("renders monochromatic icon-only action buttons without text labels", () => {
    const item: MediaOutputItem = {
      outputPath: "C:/out/sample.png",
      outputUrl: "blob:http://localhost/sample-1",
      mimeType: "image/png",
      asset_id: "ast_abc123",
    };

    const onDownload = vi.fn();
    const onSendToWatermark = vi.fn();
    const onAskAgent = vi.fn();

    const html = renderToStaticMarkup(
      <MediaOutputNode
        kind="image"
        item={item}
        onDownload={onDownload}
        onSendToWatermark={onSendToWatermark}
        onAskAgent={onAskAgent}
      />
    );

    expect(html).toContain('title="Send to Watermark"');
    expect(html).toContain('title="Ask AI Agent"');
    expect(html).toContain('title="Copy Path"');
    expect(html).toContain('title="Download"');
    // Ensure no redundant text buttons were rendered in the actions
    expect(html).not.toContain("Button--secondary");
  });
});
