package mediagen

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"app/internal/apperr"
)

func TestNormalizeImageRequest(t *testing.T) {
	got, err := NormalizeImageRequest(ImageRequest{
		Prompt:          "  a cat on a surfboard  ",
		ReferenceImages: []string{"  ", "/tmp/a.png  "},
		MaskPath:        " /tmp/mask.png ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AspectRatio != DefaultImageAspectRatio || got.Resolution != DefaultImageResolution {
		t.Fatalf("unexpected defaults: aspect=%s res=%s", got.AspectRatio, got.Resolution)
	}
	if len(got.ReferenceImages) != 1 || got.ReferenceImages[0] != "/tmp/a.png" || got.MaskPath != "/tmp/mask.png" {
		t.Fatalf("unexpected refs/mask: %v mask=%q", got.ReferenceImages, got.MaskPath)
	}

	if _, err := NormalizeImageRequest(ImageRequest{}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for empty prompt, got %v", err)
	}
	if _, err := NormalizeImageRequest(ImageRequest{Prompt: "x", AspectRatio: "99:1"}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for bad aspect ratio, got %v", err)
	}
}

func TestNormalizeVideoRequest(t *testing.T) {
	got, err := NormalizeVideoRequest(VideoRequest{
		Prompt: "drone flyover",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AspectRatio != DefaultVideoAspectRatio || got.Resolution != DefaultVideoResolution || got.DurationSec != DefaultVideoDurationSec {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	if _, err := NormalizeVideoRequest(VideoRequest{StartImage: "/tmp/start.png"}); err != nil {
		t.Fatalf("start_image alone should be valid: %v", err)
	}
	if _, err := NormalizeVideoRequest(VideoRequest{}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error when prompt and image are empty, got %v", err)
	}
	if _, err := NormalizeVideoRequest(VideoRequest{Prompt: "hi", DurationSec: 120}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for excessive duration, got %v", err)
	}
}

func TestNormalizeTTSRequest(t *testing.T) {
	got, err := NormalizeTTSRequest(TTSRequest{
		Text: "  hello world  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Format != DefaultTTSFormat || got.Speed != DefaultTTSSpeed {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	if _, err := NormalizeTTSRequest(TTSRequest{Text: "hi", Speed: 9.0}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for bad speed, got %v", err)
	}
	if _, err := NormalizeTTSRequest(TTSRequest{Text: "hi", Format: "ogg"}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for unsupported format, got %v", err)
	}
}

func TestWrapPCMToWAV(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	wav := WrapPCMToWAV(pcm, "audio/L16;codec=pcm;rate=24000;channels=1")
	if len(wav) != 48 {
		t.Fatalf("len(wav) = %d, want 48", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("invalid WAV header: %q", wav[:44])
	}
	if sr := binary.LittleEndian.Uint32(wav[24:28]); sr != 24000 {
		t.Fatalf("sample rate = %d, want 24000", sr)
	}
	if got := WrapPCMToWAV(wav, "audio/wav"); len(got) != len(wav) {
		t.Fatalf("already-wrapped WAV should not be double-wrapped")
	}
}

func TestNormalizeLipSyncRequest(t *testing.T) {
	got, err := NormalizeLipSyncRequest(LipSyncRequest{
		VideoPath: " /tmp/v.mp4 ",
		AudioPath: " /tmp/a.wav ",
		FaceIndex: -2,
	})
	if err != nil || got.VideoPath != "/tmp/v.mp4" || got.AudioPath != "/tmp/a.wav" || got.FaceIndex != 0 {
		t.Fatalf("unexpected lipsync normalized = %+v err=%v", got, err)
	}
	if _, err := NormalizeLipSyncRequest(LipSyncRequest{AudioPath: "/tmp/a.wav"}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for missing video_path, got %v", err)
	}
}

func TestSanitizeRemoteURL(t *testing.T) {
	got := SanitizeRemoteURL("https://cdn.example.com/out/video.mp4?Signature=secret&Expires=123#frag")
	if got != "https://cdn.example.com/out/video.mp4" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeTextRequestAndHelpers(t *testing.T) {
	temp := 0.7
	got, err := NormalizeTextRequest(TextRequest{
		ModelRef:    " openai/gpt-4o ",
		UserPrompt:  " Write a logline ",
		JSONSchema:  json.RawMessage(` {"type":"object"} `),
		Temperature: &temp,
		MaxTokens:   2048,
	})
	if err != nil {
		t.Fatalf("NormalizeTextRequest valid failed: %v", err)
	}
	if got.JSONSchemaName != "structured_output" || string(got.JSONSchema) != `{"type":"object"}` {
		t.Fatalf("unexpected schema normalization: %+v", got)
	}

	nan := math.NaN()
	badTemp := 2.5
	for name, req := range map[string]TextRequest{
		"empty prompt":    {},
		"invalid schema":  {UserPrompt: "x", JSONSchema: json.RawMessage(`{bad`)},
		"nan temperature": {UserPrompt: "x", Temperature: &nan},
		"high temp":       {UserPrompt: "x", Temperature: &badTemp},
		"neg max_tokens":  {UserPrompt: "x", MaxTokens: -1},
	} {
		if _, err := NormalizeTextRequest(req); !apperr.IsCode(err, apperr.CodeValidation) {
			t.Errorf("%s: expected CodeValidation, got %v", name, err)
		}
	}

	negSeed := -1
	if _, err := NormalizeImageRequest(ImageRequest{Prompt: "x", MaskPath: "/tmp/m.png"}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected mask_requires_image error, got %v", err)
	}
	if _, err := NormalizeImageRequest(ImageRequest{Prompt: "x", Seed: &negSeed}); !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected negative seed error, got %v", err)
	}

	vCustom, err := NormalizeVideoRequestWithLimits(VideoRequest{Prompt: "long shot", DurationSec: 90}, 120, 8)
	if err != nil || vCustom.DurationSec != 90 {
		t.Fatalf("NormalizeVideoRequestWithLimits(90<=120) failed: %+v err=%v", vCustom, err)
	}
	vDef, err := NormalizeVideoRequestWithLimits(VideoRequest{Prompt: "default shot"}, 120, 8)
	if err != nil || vDef.DurationSec != 8 {
		t.Fatalf("NormalizeVideoRequestWithLimits default=8 failed: %+v err=%v", vDef, err)
	}

	if w, h, ok := ParseWxH("1920*1080"); !ok || w != 1920 || h != 1080 {
		t.Fatalf("ParseWxH(1920*1080) = %dx%d ok=%v", w, h, ok)
	}
	if _, _, ok := ParseWxHWithMax("8192x8192", 4096); ok {
		t.Fatal("expected ParseWxHWithMax to enforce custom maxDim=4096")
	}
	if w, h := ImagePixelSize("16:9", "2K"); w != 2752 || h != 1536 {
		t.Fatalf("ImagePixelSize(16:9, 2K) = %dx%d, want 2752x1536", w, h)
	}
	if got := OpenAIImageSize("9:16", "1K"); got != "1024x1536" {
		t.Fatalf("OpenAIImageSize = %q, want 1024x1536", got)
	}
	if got := OpenAIVideoSize("9:16", "1080p"); got != "1024x1792" {
		t.Fatalf("OpenAIVideoSize = %q, want 1024x1792", got)
	}
	if rate, ch, bits, ok := ParsePCMMime("audio/L24;codec=pcm;rate=48000;channels=2"); !ok || rate != 48000 || ch != 2 || bits != 24 {
		t.Fatalf("ParsePCMMime = %d/%d/%d ok=%v", rate, ch, bits, ok)
	}
	if ExtForMime("video/mp4; codecs=avc1", ".bin") != ".mp4" || MimeForExt(".opus") != "audio/opus" {
		t.Fatal("unexpected ExtForMime / MimeForExt result")
	}
}
