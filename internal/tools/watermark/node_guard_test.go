package watermark_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"app/internal/apperr"
	"app/internal/engine"
	"app/internal/progress"
	"app/internal/tools/watermark"
)

func runNode(t *testing.T, ctx context.Context, opts watermark.Options, input string) (watermark.RemoverOutput, error) {
	t.Helper()
	raw, err := watermark.NewNode(opts).Run(ctx, json.RawMessage(input))
	var out watermark.RemoverOutput
	if raw != nil {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, &out)
	}
	return out, err
}

// A file guard on the context (headless server, remote client) must stop the node from reading
// files the client may not touch, before any byte is read.
func TestWatermarkRemoverNode_FileGuardDeniesRead(t *testing.T) {
	dir := t.TempDir()
	img := createWatermarkedPNG(t, dir, "secret.png")
	before, _ := os.ReadFile(img)

	var seen []string
	guard := func(p string, write bool) error {
		seen = append(seen, p)
		return errors.New("access denied: outside trusted folders")
	}
	ctx := engine.WithFileGuard(context.Background(), guard)
	out, err := runNode(t, ctx, watermark.Options{},
		fmt.Sprintf(`{"image": %q, "inplace": true}`, filepath.ToSlash(img)))
	if err == nil {
		t.Fatalf("expected error when the only input is denied")
	}
	if len(out.Results) != 1 || !strings.Contains(out.Results[0].Error, "access denied") {
		t.Fatalf("result must carry the guard's reason, got %+v", out.Results)
	}
	if len(seen) != 1 || seen[0] != filepath.Clean(img) {
		t.Fatalf("guard must see the resolved input path once, saw %v", seen)
	}
	after, _ := os.ReadFile(img)
	if !bytes.Equal(before, after) {
		t.Fatal("denied in-place run must leave the original untouched")
	}
}

// Reading may be allowed while writing into a folder is not: no output may appear there.
func TestWatermarkRemoverNode_FileGuardDeniesWrite(t *testing.T) {
	src := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "forbidden")
	img := createWatermarkedPNG(t, src, "a.png")

	guard := func(p string, write bool) error {
		if write {
			return errors.New("access denied: write")
		}
		return nil
	}
	ctx := engine.WithFileGuard(context.Background(), guard)
	out, err := runNode(t, ctx, watermark.Options{},
		fmt.Sprintf(`{"image": %q, "output_dir": %q}`, filepath.ToSlash(img), filepath.ToSlash(outDir)))
	if err == nil || len(out.Images) != 0 {
		t.Fatalf("expected the write to be refused, got err=%v images=%v", err, out.Images)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Fatalf("denied output directory must not be created (stat err=%v)", statErr)
	}
}

// Without a guard (desktop) the node keeps working on any local path; progress is reported per
// image with the final update at 100%, including items that failed to read.
func TestWatermarkRemoverNode_StreamingProgressAndOrder(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range 6 {
		paths = append(paths, filepath.ToSlash(createWatermarkedPNG(t, dir, fmt.Sprintf("img_%d.png", i))))
	}
	missing := filepath.ToSlash(filepath.Join(dir, "missing.png"))
	all := append([]string{paths[0], missing}, paths[1:]...)
	list, _ := json.Marshal(all)

	var (
		mu      sync.Mutex
		updates []progress.Update
	)
	ctx := progress.With(context.Background(), progress.Sink{Report: func(u progress.Update) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	}})
	out, err := runNode(t, ctx, watermark.Options{OutputDir: t.TempDir()},
		fmt.Sprintf(`{"images": %s, "workers": 3}`, list))
	if err != nil {
		t.Fatalf("partial failure must not fail the run: %v", err)
	}
	if out.TotalCount != len(all) || len(out.Results) != len(all) {
		t.Fatalf("expected %d results, got total=%d results=%d", len(all), out.TotalCount, len(out.Results))
	}
	for i, r := range out.Results {
		if r.Path != all[i] {
			t.Fatalf("result %d out of order: %q != %q", i, r.Path, all[i])
		}
	}
	if out.Results[1].Error == "" {
		t.Fatal("missing file must report an error")
	}
	if len(out.Images) != len(paths) {
		t.Fatalf("expected %d outputs, got %d", len(paths), len(out.Images))
	}
	if len(updates) != len(all) || updates[len(updates)-1].Percent != 100 {
		t.Fatalf("expected %d progress updates ending at 100%%, got %+v", len(all), updates)
	}
}

// A relative output_dir is anchored to the media folder (like the inputs): the guard is asked about,
// and the node writes into, the same absolute folder — never the process working directory.
func TestWatermarkRemoverNode_RelativeOutputDirAnchoredToMedia(t *testing.T) {
	media := t.TempDir()
	createWatermarkedPNG(t, media, "a.png")
	want := filepath.Join(media, "out")

	var writes []string
	guard := func(p string, write bool) error {
		if write {
			writes = append(writes, p)
		}
		return nil
	}
	ctx := engine.WithFileGuard(context.Background(), guard)
	out, err := runNode(t, ctx, watermark.Options{MediaDir: media}, `{"image": "a.png", "output_dir": "out"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) == 0 || writes[0] != want {
		t.Fatalf("guard must see the anchored folder %q, saw %v", want, writes)
	}
	if len(out.Images) != 1 || filepath.Dir(out.Images[0].LocalURL) != want {
		t.Fatalf("output must land in %q, got %+v", want, out.Images)
	}
	if _, err := os.Stat("out"); !os.IsNotExist(err) {
		t.Fatal("nothing may be created in the working directory")
	}
}

// Without a media folder a relative output_dir has no safe anchor and is rejected.
func TestWatermarkRemoverNode_RelativeOutputDirWithoutMediaRejected(t *testing.T) {
	img := createWatermarkedPNG(t, t.TempDir(), "a.png")
	_, err := runNode(t, context.Background(), watermark.Options{},
		fmt.Sprintf(`{"image": %q, "output_dir": "out"}`, filepath.ToSlash(img)))
	if err == nil || !strings.Contains(err.Error(), "output_dir") {
		t.Fatalf("expected output_dir validation error, got %v", err)
	}
}

func TestWatermarkRemoverNode_FileGuardReadErrorSanitizesDirAndReturnsUnavailable(t *testing.T) {
	dir := t.TempDir()
	img := createWatermarkedPNG(t, dir, "secret.png")

	guard := func(p string, write bool) error {
		return fmt.Errorf("access denied: %q is outside DezuxkStudio's media, workspace and trusted folders", p)
	}
	ctx := engine.WithFileGuard(context.Background(), guard)
	out, err := runNode(t, ctx, watermark.Options{}, fmt.Sprintf(`{"image": %q}`, filepath.ToSlash(img)))
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected ERR_UNAVAILABLE, got %v", err)
	}
	if len(out.Results) != 1 || !strings.Contains(out.Results[0].Error, "access denied") || !strings.Contains(out.Results[0].Error, "secret.png") {
		t.Fatalf("expected sanitized access denied with basename, got %+v", out.Results)
	}
	escapedDir := strings.TrimSuffix(strings.TrimPrefix(strconv.Quote(dir), `"`), `"`)
	for _, leak := range []string{dir, filepath.ToSlash(dir), escapedDir} {
		if strings.Contains(out.Results[0].Error, leak) || strings.Contains(err.Error(), leak) {
			t.Fatalf("guard read error leaked directory %q: itemErr=%q runErr=%q", leak, out.Results[0].Error, err.Error())
		}
	}
}

func TestWatermarkRemoverNode_FileGuardWriteErrorSanitizesDirAndReturnsUnavailable(t *testing.T) {
	src := t.TempDir()
	outParent := t.TempDir()
	outDir := filepath.Join(outParent, "forbidden")
	img := createWatermarkedPNG(t, src, "a.png")

	guard := func(p string, write bool) error {
		if write {
			return fmt.Errorf("access denied: %q in %s is outside trusted folders", p, filepath.Dir(p))
		}
		return nil
	}
	ctx := engine.WithFileGuard(context.Background(), guard)
	out, err := runNode(t, ctx, watermark.Options{},
		fmt.Sprintf(`{"image": %q, "output_dir": %q}`, filepath.ToSlash(img), filepath.ToSlash(outDir)))
	if !apperr.IsCode(err, apperr.CodeUnavailable) {
		t.Fatalf("expected ERR_UNAVAILABLE, got %v", err)
	}
	if len(out.Results) != 1 || !strings.Contains(out.Results[0].Error, "access denied") || !strings.Contains(out.Results[0].Error, "forbidden") {
		t.Fatalf("expected sanitized write denial with leaf folder, got %+v", out.Results)
	}
	escapedParent := strings.TrimSuffix(strings.TrimPrefix(strconv.Quote(outParent), `"`), `"`)
	for _, leak := range []string{outParent, filepath.ToSlash(outParent), escapedParent} {
		if strings.Contains(out.Results[0].Error, leak) || strings.Contains(err.Error(), leak) {
			t.Fatalf("guard write error leaked parent directory %q: itemErr=%q runErr=%q", leak, out.Results[0].Error, err.Error())
		}
	}
}
