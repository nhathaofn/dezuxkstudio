package watermark

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgwatermark "app/pkg/watermark"
)

func TestResolveFilePath_AllowsLeadingDoubleDotsFilename(t *testing.T) {
	mediaDir := filepath.Join(t.TempDir(), "media")
	got := resolveFilePath("..foo.png", mediaDir)
	want := filepath.Join(mediaDir, "..foo.png")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if bad := resolveFilePath("../escape.png", mediaDir); bad != "" {
		t.Fatalf("expected traversal outside mediaDir to return empty, got %q", bad)
	}
	if bad := resolveFilePath("..", mediaDir); bad != "" {
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
