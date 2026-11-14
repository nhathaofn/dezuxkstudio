package engine

import (
	"path/filepath"
	"strings"
)

// ResolveMediaPath resolves a media file path or /media/... URL against mediaDir.
// Empty inputs, ".", and relative paths that escape mediaDir return "".
// Absolute paths (checked after stripping any leading /media/ or media/ prefix) are cleaned and returned.
func ResolveMediaPath(raw, mediaDir string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Strip /media/ or media/ URL prefixes before filepath.IsAbs so "/media/x.png"
	// resolves inside mediaDir on POSIX as well as Windows.
	trimmed := strings.TrimPrefix(strings.TrimPrefix(s, "/"), `\`)
	hasMediaPrefix := false
	if strings.HasPrefix(trimmed, "media/") || strings.HasPrefix(trimmed, `media\`) {
		trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "media/"), `media\`)
		trimmed = strings.TrimLeft(trimmed, `/\`)
		if trimmed == "" {
			return ""
		}
		s = trimmed
		hasMediaPrefix = true
	}

	cleaned := filepath.Clean(s)
	if cleaned == "" || cleaned == "." {
		return ""
	}
	if !hasMediaPrefix && filepath.IsAbs(cleaned) {
		return cleaned
	}
	if hasMediaPrefix && filepath.IsAbs(cleaned) {
		return ""
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return ""
	}

	mediaDir = strings.TrimSpace(mediaDir)
	if mediaDir == "" {
		return cleaned
	}

	joined := filepath.Join(mediaDir, cleaned)
	rel, err := filepath.Rel(mediaDir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return joined
}
