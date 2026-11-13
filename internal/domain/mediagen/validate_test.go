package mediagen

import (
	"encoding/binary"
	"testing"

	"app/internal/apperr"
)

func TestNormalizeImageRequest(t *testing.T) {
	got, err := NormalizeImageRequest(ImageRequest{
		Prompt:          "  a cat on a surfboard  ",
		ReferenceImages: []string{"  ", "/tmp/a.png  "},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.AspectRatio != DefaultImageAspectRatio || got.Resolution != DefaultImageResolution {
		t.Fatalf("unexpected defaults: aspect=%s res=%s", got.AspectRatio, got.Resolution)
	}
	if len(got.ReferenceImages) != 1 || got.ReferenceImages[0] != "/tmp/a.png" {
		t.Fatalf("unexpected refs: %v", got.ReferenceImages)
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

func TestSanitizeRemoteURL(t *testing.T) {
	got := SanitizeRemoteURL("https://cdn.example.com/out/video.mp4?Signature=secret&Expires=123#frag")
	if got != "https://cdn.example.com/out/video.mp4" {
		t.Fatalf("got %q", got)
	}
}
