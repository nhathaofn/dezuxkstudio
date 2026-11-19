package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// with or without file extensions (e.g. CON, con.txt, PRN, AUX, NUL, COM1-9, LPT1-9).
func isDOSDeviceSegment(seg string) bool {
	s := strings.Trim(seg, " \t\r\n.:")
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if dosDeviceNames[lower] {
		return true
	}
	if idx := strings.IndexByte(lower, '.'); idx != -1 {
		base := strings.Trim(lower[:idx], " \t\r\n.:")
		if dosDeviceNames[base] {
			return true
		}
	}
	return false
}

// containsDOSDeviceSegment reports whether any segment of path p matches a Windows DOS reserved device name.
func containsDOSDeviceSegment(p string) bool {
	clean := strings.ReplaceAll(p, `\`, `/`)
	if len(clean) >= 2 && clean[1] == ':' && ((clean[0] >= 'a' && clean[0] <= 'z') || (clean[0] >= 'A' && clean[0] <= 'Z')) {
		clean = clean[2:]
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if isDOSDeviceSegment(part) {
			return true
		}
	}
	return false
}

var internalProtectedSubdirs = []string{
	"backups", "diagnostics", "logs", "secrets", "tools", "agent-profiles", "agent-customizations", "agent-cli",
}

// NewAccessPolicy constructs the policy. allowedDirs are user-granted trusted folders.

func (p *AccessPolicy) windowsPathProblem(path string, allowNetwork bool) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return windowsPathProblemFor(path, allowNetwork || p.underTrustedShare(path))
}

func windowsPathProblemFor(path string, allowNetwork bool) string {
	slashed := strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(slashed, `\\?\`) || strings.HasPrefix(slashed, `\\.\`) || strings.HasPrefix(slashed, `\??\`) {
		return `uses a Windows device path; use a normal path like D:\Folder`
	}
	if strings.HasPrefix(slashed, `\\`) && !allowNetwork {
		return "is a network location; add the shared folder as a trusted folder in DezuxkStudio first"
	}
	if strings.Contains(path[len(filepath.VolumeName(path)):], ":") {
		return "contains ':' (alternate data streams are not allowed)"
	}
	if containsDOSDeviceSegment(path) {
		return "contains a Windows reserved device name"
	}
	return ""
}

// underTrustedShare reports whether path lies inside a user-trusted folder on a network share.
func (p *AccessPolicy) underTrustedShare(path string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, d := range p.trustedDirs {
		if strings.HasPrefix(strings.ReplaceAll(d, "/", `\`), `\\`) && isWithinDir(path, d) {
			return true
		}
	}
	return false
}

func (p *AccessPolicy) protectedReason(resolved string) string {
	if hasForbiddenSegment(resolved, p.isDeveloperMode()) {
		return "inside a protected folder (credentials, developer or system data)"
	}
	if p.dataDir != "" && isWithinDir(resolved, p.dataDir) {
		base := strings.ToLower(filepath.Base(resolved))
		if strings.HasPrefix(base, "app.db") || strings.HasSuffix(base, ".db") {
			return "DezuxkStudio's internal database"
		}
		if pathKey(resolved) == pathKey(p.dataDir) {
			return "DezuxkStudio's internal data directory"
		}
		for _, sub := range internalProtectedSubdirs {
			if isWithinDir(resolved, filepath.Join(p.dataDir, sub)) {
				return "DezuxkStudio's internal " + sub + " location"
			}
		}
	}
	trusted := p.IsTrusted(resolved)
	for _, dir := range p.protected {
		if !isWithinDir(resolved, dir) {
			continue
		}
		// DezuxkStudio's internal dirs are always protected; OS/app-data dirs are carved out for trusted roots
		// (e.g. the media folder that lives under %APPDATA%\DezuxkStudio).
		if trusted && !p.isInternalDir(dir) {
			continue
		}
		return "a protected system, application-data, or DezuxkStudio internal location"
	}
	return ""
}

func (p *AccessPolicy) isInternalDir(dir string) bool {
	if p.profilesDir != "" && pathKey(dir) == pathKey(p.profilesDir) {
		return true
	}
	return p.dataDir != "" && isWithinDir(dir, p.dataDir) && pathKey(dir) != pathKey(p.dataDir)
}

func (p *AccessPolicy) VisibleInListing(full string, isDir bool) bool {
	if p.protectedReason(full) != "" {
		return false
	}
	return isDir || p.classifyForPolicy(full) != FileClassForbidden
}

func hasForbiddenSegment(path string, devMode bool) bool {
	for _, seg := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		lower := strings.ToLower(seg)
		if !forbiddenSegmentNames[lower] {
			continue
		}
		if devMode && developerConfigSegmentNames[lower] {
			continue
		}
		return true
	}
	return false
}

func isVolumeRoot(p string) bool {
	clean := filepath.Clean(p)
	if filepath.Dir(clean) == clean {
		return true
	}
	vol := filepath.VolumeName(clean)
	if vol != "" && (clean == vol || clean == vol+string(filepath.Separator) || clean == vol+`\`) {
		return true
	}
	return clean == "/" || clean == `\`
}

func resolvePathFollowAncestors(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)

	// Walk up until an existing ancestor can be EvalSymlinks'd, then re-attach missing tail segments.
	cur := abs
	var tail []string
	for {
		realPath, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{realPath}, tail...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

func isWithinDir(target, root string) bool {
	if root == "" {
		return false
	}
	tNorm := pathKey(target)
	rNorm := pathKey(root)
	if tNorm == rNorm {
		return true
	}
	rel, err := filepath.Rel(rNorm, tNorm)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func normalizeAbsPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(abs)
}

func pathKey(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

func isExplicitlyForbiddenBase(base string) bool {
	return forbiddenFileNames[base] ||
		base == ".env" || strings.HasPrefix(base, ".env.") ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa")
}

// CheckTrustedFile is an engine.FileGuard for clients that must stay inside the trusted roots (workspace,
// projects, media, staging, user-granted folders): the path must pass the policy, stay outside protected
// directories, and never target forbidden secret/code/binary/database files.
// Returned errors never expose server host directory paths in their message.
