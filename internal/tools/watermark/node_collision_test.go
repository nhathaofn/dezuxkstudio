package watermark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"app/internal/engine"
	pkgwatermark "app/pkg/watermark"
)

func TestResolveFilePath_AllowsLeadingDoubleDotsFilename(t *testing.T) {
	mediaDir := filepath.Join(t.TempDir(), "media")
	got := engine.ResolveMediaPath("..foo.png", mediaDir)
	want := filepath.Join(mediaDir, "..foo.png")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if bad := engine.ResolveMediaPath("../escape.png", mediaDir); bad != "" {
		t.Fatalf("expected traversal outside mediaDir to return empty, got %q", bad)
	}
	if bad := engine.ResolveMediaPath("..", mediaDir); bad != "" {
		t.Fatalf("expected .. to return empty, got %q", bad)
	}
}

func TestFinishItem_InPlaceExtensionChangeRefusesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	srcWebp := filepath.Join(dir, "sample.webp")
	existingPng := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(srcWebp, []byte("webp-original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingPng, []byte("png-precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	br := pkgwatermark.BatchResult{
		Detected:     true,
		Score:        0.95,
		Output:       []byte("new-png-data"),
		OutputFormat: pkgwatermark.FormatPNG,
		Info:         pkgwatermark.Info{Size: 48},
	}
	res, outPath := finishItem(context.Background(), RemoverInput{InPlace: true}, "", srcWebp, srcWebp, br)
	if outPath != "" || !strings.Contains(res.Error, "already exists") {
		t.Fatalf("expected collision error when target .png exists, got outPath=%q res=%+v", outPath, res)
	}
	if raw, err := os.ReadFile(srcWebp); err != nil || string(raw) != "webp-original" {
		t.Fatalf("source .webp must remain intact, got %q err=%v", string(raw), err)
	}
	if raw, err := os.ReadFile(existingPng); err != nil || string(raw) != "png-precious" {
		t.Fatalf("existing .png must not be overwritten, got %q err=%v", string(raw), err)
	}
}

func TestRemoverInput_JSONMarshalUnmarshal(t *testing.T) {
	in := RemoverInput{
		Image: RemoverImage{
			MediaRef: engine.MediaRef{LocalURL: "one.png", Kind: engine.MediaImage, MimeType: "image/png"},
		},
		Images: RemoverImages{
			{LocalURL: "two.jpg", Kind: engine.MediaImage},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal RemoverInput: %v", err)
	}
	var out RemoverInput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal RemoverInput: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", out, in)
	}
}
