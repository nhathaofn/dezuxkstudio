package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// IsSubpath reports whether target lies within baseDir.
// When strict is true, target must be strictly inside baseDir (target != baseDir).
func IsSubpath(baseDir, target string, strict bool) bool {
	absBase, err := filepath.Abs(strings.TrimSpace(baseDir))
	if err != nil {
		absBase = filepath.Clean(baseDir)
	}
	absTarget, err := filepath.Abs(strings.TrimSpace(target))
	if err != nil {
		absTarget = filepath.Clean(target)
	}
	if absBase == "" || absTarget == "" {
		return false
	}

	if runtime.GOOS == "windows" {
		if !strings.EqualFold(filepath.VolumeName(absBase), filepath.VolumeName(absTarget)) {
			return false
		}
		absBase = strings.ToLower(absBase)
		absTarget = strings.ToLower(absTarget)
	}

	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	if rel == "." {
		return !strict
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// ResolveSubpath resolves symlinks/junctions for baseDir and target (or its deepest
// existing ancestor when target does not exist yet) and verifies that the resolved
// target remains within the resolved baseDir.
func ResolveSubpath(baseDir, target string, strict bool) (realTarget, realBase string, err error) {
	absBase, err := filepath.Abs(strings.TrimSpace(baseDir))
	if err != nil {
		return "", "", fmt.Errorf("fsutil: resolve base %q: %w", baseDir, err)
	}
	absTarget, err := filepath.Abs(strings.TrimSpace(target))
	if err != nil {
		return "", "", fmt.Errorf("fsutil: resolve target %q: %w", target, err)
	}

	realBase, err = evalExistingPrefix(absBase)
	if err != nil {
		return "", "", fmt.Errorf("fsutil: eval base %q: %w", baseDir, err)
	}
	realTarget, err = evalExistingPrefix(absTarget)
	if err != nil {
		return "", "", fmt.Errorf("fsutil: eval target %q: %w", target, err)
	}

	if !IsSubpath(realBase, realTarget, strict) {
		return "", "", fmt.Errorf("fsutil: path %q escapes %q", target, baseDir)
	}
	return realTarget, realBase, nil
}

func evalExistingPrefix(absPath string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return filepath.Clean(resolved), nil
	}
	cur := filepath.Clean(absPath)
	var tail []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(absPath), nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			out := filepath.Clean(resolved)
			for i := len(tail) - 1; i >= 0; i-- {
				out = filepath.Join(out, tail[i])
			}
			return filepath.Clean(out), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
}
