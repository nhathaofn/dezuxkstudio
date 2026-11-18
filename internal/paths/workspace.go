package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"app/internal/buildinfo"
)

// WorkspaceTree holds the single source of truth for creative and agent workspace directories.
type WorkspaceTree struct {
	Root         string
	MediaDir     string
	ImagesDir    string
	VideosDir    string
	TTSDir       string
	WatermarkDir string
	WorkspaceDir string
	ProjectsDir  string
}

func cleanAbsPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

// ResolveWorkspaceTree calculates all creative workspace directories based on customRoot
// and an optional agent workspace override.
// If customRoot is empty or whitespace, it falls back to appPaths.DefaultStudioRoot().
func ResolveWorkspaceTree(appPaths Paths, customRoot, agentWorkspaceOverride string) WorkspaceTree {
	var root string
	if absRoot := cleanAbsPath(customRoot); absRoot != "" {
		root = absRoot
	} else {
		root = cleanAbsPath(appPaths.DefaultStudioRoot())
	}

	mediaDir := filepath.Join(root, "media")
	imagesDir := filepath.Join(mediaDir, "images")
	videosDir := filepath.Join(mediaDir, "videos")
	ttsDir := filepath.Join(mediaDir, "tts")
	watermarkDir := filepath.Join(mediaDir, "watermark")

	var workspaceDir string
	if absOverride := cleanAbsPath(agentWorkspaceOverride); absOverride != "" {
		workspaceDir = absOverride
	} else {
		workspaceDir = filepath.Join(root, "workspace")
	}

	projectsDir := filepath.Join(workspaceDir, "projects")

	return WorkspaceTree{
		Root:         root,
		MediaDir:     mediaDir,
		ImagesDir:    imagesDir,
		VideosDir:    videosDir,
		TTSDir:       ttsDir,
		WatermarkDir: watermarkDir,
		WorkspaceDir: workspaceDir,
		ProjectsDir:  projectsDir,
	}
}

// Ensure creates all 8 directories in the workspace tree with 0o755 permissions.
func (t WorkspaceTree) Ensure() error {
	dirs := []string{
		t.Root,
		t.MediaDir,
		t.ImagesDir,
		t.VideosDir,
		t.TTSDir,
		t.WatermarkDir,
		t.WorkspaceDir,
		t.ProjectsDir,
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("paths: create %s: %w", dir, err)
		}
	}
	return nil
}

// SuggestedWorkspaceRoot calculates a recommended default workspace root dynamically:
// <UserHome>/Documents/<productName> if Documents exists and is a directory;
// otherwise <UserHome>/<productName>.
// If productName is empty or whitespace, buildinfo.ProductName is used.
// Zero hardcoded strings or Windows paths.
func SuggestedWorkspaceRoot(productName string) string {
	name := strings.TrimSpace(productName)
	if name == "" {
		name = strings.TrimSpace(buildinfo.ProductName)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	docs := filepath.Join(home, "Documents")
	if fi, err := os.Stat(docs); err == nil && fi.IsDir() {
		return filepath.Join(docs, name)
	}
	return filepath.Join(home, name)
}
