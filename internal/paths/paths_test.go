package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSeparatesDirectoriesAndHonoursOverrides(t *testing.T) {
	p, err := Resolve("TestApp", Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Config, p.Data, p.Logs} {
		if !filepath.IsAbs(d) || filepath.Base(filepath.Dir(d)) == "" {
			t.Fatalf("not absolute: %+v", p)
		}
	}
	if p.Data == p.Logs {
		t.Fatal("data and logs must differ")
	}

	tmp := t.TempDir()
	o, err := Resolve("TestApp", Overrides{DataDir: filepath.Join(tmp, "d"), LogDir: filepath.Join(tmp, "l")})
	if err != nil {
		t.Fatal(err)
	}
	if o.Data != filepath.Join(tmp, "d") || o.Logs != filepath.Join(tmp, "l") || o.Config != p.Config {
		t.Fatalf("overrides not applied: %+v", o)
	}
	if err := o.Ensure(); err == nil {
		for _, d := range []string{o.Data, o.Logs, o.Backups(), o.Diagnostics()} {
			if info, err := os.Stat(d); err != nil || !info.IsDir() {
				t.Errorf("missing dir %s", d)
			}
		}
	}

	if _, err := Resolve("", Overrides{}); err == nil {
		t.Fatal("empty app id accepted")
	}
}

func TestResolveWorkspaceTree(t *testing.T) {
	appPaths, err := Resolve("TestApp", Overrides{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default studio root when customRoot is empty or whitespace", func(t *testing.T) {
		for _, emptyRoot := range []string{"", "   ", "\t\n"} {
			tree := ResolveWorkspaceTree(appPaths, emptyRoot, "")
			expectedRoot := appPaths.DefaultStudioRoot()
			if tree.Root != expectedRoot {
				t.Fatalf("expected root %q, got %q", expectedRoot, tree.Root)
			}
			if tree.MediaDir != filepath.Join(expectedRoot, "media") {
				t.Fatalf("expected media dir %q, got %q", filepath.Join(expectedRoot, "media"), tree.MediaDir)
			}
			if tree.ImagesDir != filepath.Join(expectedRoot, "media", "images") {
				t.Fatalf("expected images dir %q, got %q", filepath.Join(expectedRoot, "media", "images"), tree.ImagesDir)
			}
			if tree.VideosDir != filepath.Join(expectedRoot, "media", "videos") {
				t.Fatalf("expected videos dir %q, got %q", filepath.Join(expectedRoot, "media", "videos"), tree.VideosDir)
			}
			if tree.TTSDir != filepath.Join(expectedRoot, "media", "tts") {
				t.Fatalf("expected tts dir %q, got %q", filepath.Join(expectedRoot, "media", "tts"), tree.TTSDir)
			}
			if tree.WatermarkDir != filepath.Join(expectedRoot, "media", "watermark") {
				t.Fatalf("expected watermark dir %q, got %q", filepath.Join(expectedRoot, "media", "watermark"), tree.WatermarkDir)
			}
			if tree.WorkspaceDir != filepath.Join(expectedRoot, "workspace") {
				t.Fatalf("expected workspace dir %q, got %q", filepath.Join(expectedRoot, "workspace"), tree.WorkspaceDir)
			}
			if tree.ProjectsDir != filepath.Join(expectedRoot, "workspace", "projects") {
				t.Fatalf("expected projects dir %q, got %q", filepath.Join(expectedRoot, "workspace", "projects"), tree.ProjectsDir)
			}
		}
	})

	t.Run("custom root is cleaned and used", func(t *testing.T) {
		tmp := t.TempDir()
		custom := filepath.Join(tmp, "custom_root")
		tree := ResolveWorkspaceTree(appPaths, "  "+custom+"  ", "")
		if tree.Root != custom {
			t.Fatalf("expected root %q, got %q", custom, tree.Root)
		}
		if tree.MediaDir != filepath.Join(custom, "media") {
			t.Fatalf("expected media %q, got %q", filepath.Join(custom, "media"), tree.MediaDir)
		}
		if tree.ImagesDir != filepath.Join(custom, "media", "images") {
			t.Fatalf("expected images %q, got %q", filepath.Join(custom, "media", "images"), tree.ImagesDir)
		}
		if tree.VideosDir != filepath.Join(custom, "media", "videos") {
			t.Fatalf("expected videos %q, got %q", filepath.Join(custom, "media", "videos"), tree.VideosDir)
		}
		if tree.TTSDir != filepath.Join(custom, "media", "tts") {
			t.Fatalf("expected tts %q, got %q", filepath.Join(custom, "media", "tts"), tree.TTSDir)
		}
		if tree.WatermarkDir != filepath.Join(custom, "media", "watermark") {
			t.Fatalf("expected watermark %q, got %q", filepath.Join(custom, "media", "watermark"), tree.WatermarkDir)
		}
		if tree.WorkspaceDir != filepath.Join(custom, "workspace") {
			t.Fatalf("expected workspace %q, got %q", filepath.Join(custom, "workspace"), tree.WorkspaceDir)
		}
		if tree.ProjectsDir != filepath.Join(custom, "workspace", "projects") {
			t.Fatalf("expected projects %q, got %q", filepath.Join(custom, "workspace", "projects"), tree.ProjectsDir)
		}
	})

	t.Run("agent workspace override", func(t *testing.T) {
		tmp := t.TempDir()
		customRoot := filepath.Join(tmp, "custom_root")
		agentOverride := filepath.Join(tmp, "external_agent_workspace")
		tree := ResolveWorkspaceTree(appPaths, customRoot, "  "+agentOverride+"  ")
		if tree.Root != customRoot {
			t.Fatalf("expected root %q, got %q", customRoot, tree.Root)
		}
		if tree.WorkspaceDir != agentOverride {
			t.Fatalf("expected override workspace %q, got %q", agentOverride, tree.WorkspaceDir)
		}
		if tree.ProjectsDir != filepath.Join(agentOverride, "projects") {
			t.Fatalf("expected override projects %q, got %q", filepath.Join(agentOverride, "projects"), tree.ProjectsDir)
		}
	})

	t.Run("relative customRoot and agentWorkspaceOverride resolve to absolute paths", func(t *testing.T) {
		tree := ResolveWorkspaceTree(appPaths, "rel_studio_root", "rel_agent_ws")
		if !filepath.IsAbs(tree.Root) || !filepath.IsAbs(tree.WorkspaceDir) || !filepath.IsAbs(tree.ProjectsDir) {
			t.Fatalf("expected absolute paths from relative inputs, got root=%q ws=%q proj=%q", tree.Root, tree.WorkspaceDir, tree.ProjectsDir)
		}
	})
}

func TestWorkspaceTreeEnsure(t *testing.T) {
	tmp := t.TempDir()
	appPaths, err := Resolve("TestApp", Overrides{DataDir: filepath.Join(tmp, "app_data")})
	if err != nil {
		t.Fatal(err)
	}
	customRoot := filepath.Join(tmp, "my_custom_workspace")
	agentOverride := filepath.Join(tmp, "my_override_workspace")

	tree := ResolveWorkspaceTree(appPaths, customRoot, agentOverride)
	if err := tree.Ensure(); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}

	for _, d := range []string{
		tree.Root,
		tree.MediaDir,
		tree.ImagesDir,
		tree.VideosDir,
		tree.TTSDir,
		tree.WatermarkDir,
		tree.WorkspaceDir,
		tree.ProjectsDir,
	} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected directory %q to exist: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("expected %q to be directory", d)
		}
	}
}

func TestSuggestedWorkspaceRoot(t *testing.T) {
	// Fallback to buildinfo.ProductName
	suggestedDefault := SuggestedWorkspaceRoot("")
	if suggestedDefault == "" {
		t.Fatal("suggested workspace root must not be empty")
	}
	if !filepath.IsAbs(suggestedDefault) {
		t.Fatalf("expected absolute path, got %q", suggestedDefault)
	}

	// Custom product name
	customName := "CustomTestStudio"
	suggestedCustom := SuggestedWorkspaceRoot(customName)
	if filepath.Base(suggestedCustom) != customName {
		t.Fatalf("expected base %q, got %q", customName, filepath.Base(suggestedCustom))
	}
	if !filepath.IsAbs(suggestedCustom) {
		t.Fatalf("expected absolute path, got %q", suggestedCustom)
	}
}

func TestPathsStagingAndDefaultStudioRoot(t *testing.T) {
	tmp := t.TempDir()
	p, err := Resolve("TestApp", Overrides{DataDir: filepath.Join(tmp, "data"), LogDir: filepath.Join(tmp, "logs")})
	if err != nil {
		t.Fatal(err)
	}

	if p.Staging() != filepath.Join(p.Data, "staging") {
		t.Fatalf("expected staging %q, got %q", filepath.Join(p.Data, "staging"), p.Staging())
	}
	if p.MediaGenStaging() != filepath.Join(p.Data, "staging", "mediagen") {
		t.Fatalf("expected mediagen staging %q, got %q", filepath.Join(p.Data, "staging", "mediagen"), p.MediaGenStaging())
	}
	if p.DefaultStudioRoot() != filepath.Join(p.Data, "studio") {
		t.Fatalf("expected studio root %q, got %q", filepath.Join(p.Data, "studio"), p.DefaultStudioRoot())
	}

	if err := p.Ensure(); err != nil {
		t.Fatalf("Ensure failed: %v", err)
	}
	if fi, err := os.Stat(p.Staging()); err != nil || !fi.IsDir() {
		t.Fatalf("staging dir %q missing after Ensure: %v", p.Staging(), err)
	}
	if _, err := os.Stat(p.Media()); !os.IsNotExist(err) {
		t.Fatalf("legacy media dir %q must not be pre-created on fresh Ensure()", p.Media())
	}
}
