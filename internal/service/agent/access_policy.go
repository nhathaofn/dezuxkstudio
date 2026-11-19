package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"app/internal/apperr"
)

// AccessPolicy decides which files DezuxkStudio's agent may touch.
//
// The agent is strictly confined to trusted directories (DezuxkStudio's workspace, media dir,
// user-granted trusted folders, and future project folders). Any path outside trusted directories
// is denied outright. Even inside trusted directories, the policy blocks:
//   - secrets and credentials (SSH/cloud keys, .env, password stores, browser profiles, CLI agent configs);
//   - source code and executables (unless DeveloperMode is explicitly enabled);
//   - operating-system directories and DezuxkStudio's own internal data (database, isolated agent profiles, logs).
//
// Within allowed trusted directories, workspace file tools run directly without interactive file
// approval prompts; destructive engine-node parameters (e.g. InPlace overwrite or provider delete)
// still require explicit UI approval.
type AccessPolicy struct {
	mu           sync.RWMutex
	dataDir      string
	workspaceDir string
	projectsDir  string
	mediaDir     string
	stagingDir   string
	profilesDir  string
	legacyRoots  []string // preserved built-in studio/media roots across workspace hot-reloads
	trustedDirs  []string
	protected    []string // absolute directories that are always off-limits
	devMode      bool
}

// SetDeveloperMode toggles opt-in developer/coding file permissions (guarded by config).
func (p *AccessPolicy) SetDeveloperMode(enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.devMode = enabled
}

// AddProtectedDirs appends additional directories (such as custom config or log roots)
// to the protected deny-list under mutex lock.
func (p *AccessPolicy) AddProtectedDirs(dirs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]struct{}, len(p.protected))
	for _, existing := range p.protected {
		seen[pathKey(existing)] = struct{}{}
	}
	for _, d := range dirs {
		norm := normalizeAbsPath(d)
		if norm == "" {
			continue
		}
		if resolved, err := resolvePathFollowAncestors(norm); err == nil {
			norm = resolved
		}
		key := pathKey(norm)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		p.protected = append(p.protected, norm)
	}
}

func (p *AccessPolicy) isDeveloperMode() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.devMode
}

func (p *AccessPolicy) getWorkspaceDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.workspaceDir
}

func (p *AccessPolicy) getMediaDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mediaDir
}

func (p *AccessPolicy) getStagingDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stagingDir
}

// SetWorkspaceDirs updates DezuxkStudio's workspace, media, and staging directories at runtime under mutex lock.
func (p *AccessPolicy) SetWorkspaceDirs(workspaceDir, mediaDir, stagingDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workspaceDir = normalizeAbsPath(workspaceDir)
	p.mediaDir = normalizeAbsPath(mediaDir)
	p.stagingDir = normalizeAbsPath(stagingDir)
}

// SetProjectsDir updates DezuxkStudio's active studio projects directory at runtime under mutex lock.
func (p *AccessPolicy) SetProjectsDir(projectsDir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.projectsDir = normalizeAbsPath(projectsDir)
}

// ProjectsDir returns the currently configured studio projects root directory.
func (p *AccessPolicy) ProjectsDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.projectsDir
}

// AddLegacyRoots retains previous built-in studio/media roots (including default <Data>/studio/* directories)
// so existing projects and assets remain accessible after switching WorkspaceRoot.
func (p *AccessPolicy) AddLegacyRoots(dirs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]struct{}, len(p.legacyRoots))
	for _, existing := range p.legacyRoots {
		seen[pathKey(existing)] = struct{}{}
	}
	for _, d := range dirs {
		norm := normalizeAbsPath(d)
		if norm == "" {
			continue
		}
		if resolved, err := resolvePathFollowAncestors(norm); err == nil {
			norm = resolved
		}
		key := pathKey(norm)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		p.legacyRoots = append(p.legacyRoots, norm)
	}
}

func NewAccessPolicy(dataDir, workspaceDir, mediaDir, profilesDir string, allowedDirs []string) *AccessPolicy {
	normWS := normalizeAbsPath(workspaceDir)
	projDir := ""
	if normWS != "" {
		projDir = filepath.Join(normWS, "projects")
	}
	p := &AccessPolicy{
		dataDir:      normalizeAbsPath(dataDir),
		workspaceDir: normWS,
		projectsDir:  projDir,
		mediaDir:     normalizeAbsPath(mediaDir),
		profilesDir:  normalizeAbsPath(profilesDir),
	}
	p.protected = p.buildProtectedDirs()
	p.seedExistingDefaultStudioRoots()
	p.SetAllowedDirs(allowedDirs)
	return p
}

func (p *AccessPolicy) seedExistingDefaultStudioRoots() {
	if p.dataDir == "" {
		return
	}
	studioRoot := filepath.Join(p.dataDir, "studio")
	for _, cand := range []string{
		filepath.Join(studioRoot, "workspace"),
		filepath.Join(studioRoot, "workspace", "projects"),
		filepath.Join(studioRoot, "media"),
		filepath.Join(p.dataDir, "media"),
	} {
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			p.AddLegacyRoots(cand)
		}
	}
}

func (p *AccessPolicy) buildProtectedDirs() []string {
	var dirs []string
	add := func(d string) {
		if n := normalizeAbsPath(d); n != "" {
			dirs = append(dirs, n)
		}
	}
	add(p.profilesDir)
	if p.dataDir != "" {
		for _, sub := range internalProtectedSubdirs {
			add(filepath.Join(p.dataDir, sub))
		}
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		for _, env := range []string{"SystemRoot", "windir", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData"} {
			add(os.Getenv(env))
		}
		// Application data holds browser profiles, password managers, tokens and app internals.
		// DezuxkStudio's own workspace/media/trusted dirs inside it are carved out in check().
		add(os.Getenv("APPDATA"))
		add(os.Getenv("LOCALAPPDATA"))
		if home != "" {
			add(filepath.Join(home, "AppData"))
		}
	} else {
		for _, d := range []string{"/etc", "/usr", "/bin", "/sbin", "/boot", "/proc", "/sys", "/dev", "/var", "/opt", "/private", "/System", "/Library", "/Applications"} {
			add(d)
		}
		if home != "" {
			add(filepath.Join(home, "Library"))
			add(filepath.Join(home, ".config"))
			add(filepath.Join(home, ".local", "share"))
		}
	}
	return dirs
}

// SetAllowedDirs replaces the user-granted trusted folder list (and, later, project folders).
func (p *AccessPolicy) SetAllowedDirs(dirs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cleaned := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, d := range dirs {
		norm := normalizeAbsPath(d)
		if norm == "" {
			continue
		}
		key := pathKey(norm)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, norm)
	}
	p.trustedDirs = cleaned
}

// Roots returns the trusted roots (workspace, media, staging, user-granted folders).
func (p *AccessPolicy) Roots() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []string
	seen := make(map[string]struct{})
	add := func(dir string) {
		norm := normalizeAbsPath(dir)
		if norm == "" {
			return
		}
		key := pathKey(norm)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, norm)
	}

	add(p.workspaceDir)
	add(p.projectsDir)
	add(p.mediaDir)
	add(p.stagingDir)
	for _, lr := range p.legacyRoots {
		add(lr)
	}
	for _, td := range p.trustedDirs {
		add(td)
	}
	return out
}

// IsTrusted reports whether an already-resolved path lies inside a trusted root.
func (p *AccessPolicy) IsTrusted(resolved string) bool {
	for _, root := range p.Roots() {
		if r, err := resolvePathFollowAncestors(root); err == nil && isWithinDir(resolved, r) {
			return true
		}
	}
	return false
}

// ValidateAllowedDirCandidate verifies a user-selected folder can be trusted.
func (p *AccessPolicy) ValidateAllowedDirCandidate(dir string) (string, error) {
	// The user picked this folder in the UI, so a network share is acceptable here.
	resolved, err := p.resolve(dir, true)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("access denied: %q is not an existing directory", dir)
	}
	if isVolumeRoot(resolved) {
		return "", fmt.Errorf("access denied: a whole drive cannot be marked as trusted; choose a folder")
	}
	return resolved, nil
}

// ValidateWorkspaceRootCandidate verifies a candidate master workspace root path.
// It accepts non-existent directories if their existing ancestor is valid, and rejects:
//   - empty paths or paths with NUL bytes;
//   - device namespaces (\\?\, \\.\, \??\) and network paths;
//   - NTFS Alternate Data Streams (:);
//   - Windows DOS reserved device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9);
//   - whole drive / volume roots (e.g. C:\, D:\, \, /);
//   - existing regular files;
//   - paths containing forbidden directory segments (.git, .ssh, .aws, etc.);
//   - paths inside protected system or application data folders (Windows, ProgramFiles, ProgramData, AppData, LocalAppData, dataDir).
func (p *AccessPolicy) ValidateWorkspaceRootCandidate(raw string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), `"'`)
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("access denied: workspace root candidate contains NUL bytes")
	}
	if trimmed == "" {
		return "", fmt.Errorf("access denied: workspace root cannot be empty")
	}

	if strings.HasPrefix(trimmed, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			trimmed = filepath.Join(home, strings.TrimLeft(trimmed[1:], `/\`))
		}
	}

	slashed := strings.ReplaceAll(trimmed, "/", `\`)
	if strings.HasPrefix(slashed, `\\?\`) || strings.HasPrefix(slashed, `\\.\`) || strings.HasPrefix(slashed, `\??\`) {
		return "", fmt.Errorf("access denied: %q uses a Windows device path; use a normal path", raw)
	}
	if strings.HasPrefix(slashed, `\\`) {
		return "", fmt.Errorf("access denied: %q is a network location; workspace root must be a local directory", raw)
	}
	if vol := filepath.VolumeName(trimmed); strings.Contains(trimmed[len(vol):], ":") {
		return "", fmt.Errorf("access denied: %q contains ':' (alternate data streams are not allowed)", raw)
	}

	candidate := filepath.Clean(trimmed)
	if !filepath.IsAbs(candidate) {
		return "", fmt.Errorf("access denied: workspace root must be an absolute path, got %q", raw)
	}

	if containsDOSDeviceSegment(candidate) {
		return "", fmt.Errorf("access denied: workspace root candidate %q contains a Windows reserved device name", raw)
	}

	resolved, err := resolvePathFollowAncestors(candidate)
	if err != nil {
		return "", fmt.Errorf("access denied: cannot resolve candidate path %q: %w", raw, err)
	}
	resolved = filepath.Clean(resolved)

	// Re-verify device path / network / ADS on the resolved path.
	resSlashed := strings.ReplaceAll(resolved, "/", `\`)
	if strings.HasPrefix(resSlashed, `\\?\`) || strings.HasPrefix(resSlashed, `\\.\`) || strings.HasPrefix(resSlashed, `\??\`) {
		return "", fmt.Errorf("access denied: %q resolves to a Windows device path", raw)
	}
	if strings.HasPrefix(resSlashed, `\\`) {
		return "", fmt.Errorf("access denied: %q resolves to a network location", raw)
	}
	if vol := filepath.VolumeName(resolved); strings.Contains(resolved[len(vol):], ":") {
		return "", fmt.Errorf("access denied: %q contains ':' (alternate data streams are not allowed)", raw)
	}
	if containsDOSDeviceSegment(resolved) {
		return "", fmt.Errorf("access denied: workspace root candidate %q contains a Windows reserved device name", raw)
	}

	if isVolumeRoot(resolved) {
		return "", fmt.Errorf("access denied: a whole drive cannot be used as workspace root; choose a folder")
	}

	st, err := os.Stat(resolved)
	if err == nil {
		if !st.IsDir() {
			return "", fmt.Errorf("access denied: %q is a regular file; workspace root must be a directory", raw)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("access denied: cannot access candidate path %q: %w", raw, err)
	}

	if hasForbiddenSegment(resolved, false) {
		return "", fmt.Errorf("access denied: %q contains forbidden directory names", raw)
	}

	// Check protected directories: dataDir, OS system folders, AppData
	if p.dataDir != "" && isWithinDir(resolved, p.dataDir) {
		return "", fmt.Errorf("access denied: workspace root cannot be inside DezuxkStudio internal data directory")
	}

	p.mu.RLock()
	protectedList := make([]string, len(p.protected))
	copy(protectedList, p.protected)
	p.mu.RUnlock()

	for _, dir := range protectedList {
		if isWithinDir(resolved, dir) {
			return "", fmt.Errorf("access denied: workspace root cannot be inside protected system or application data folder")
		}
	}

	return resolved, nil
}

// Resolve turns a user/agent supplied path into a clean absolute path and enforces that
// it stays inside DezuxkStudio's workspace, media, or user-trusted folders and outside protected paths.
// Relative paths are resolved against the agent workspace.
func (p *AccessPolicy) Resolve(raw string) (string, error) {
	return p.resolve(raw, false)
}

func (p *AccessPolicy) resolve(raw string, allowNetwork bool) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), `"'`)
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("access denied: invalid path %q", raw)
	}
	ws := p.getWorkspaceDir()
	if trimmed == "" {
		if ws == "" {
			return "", fmt.Errorf("access denied: path is required")
		}
		trimmed = ws
	}
	if strings.HasPrefix(trimmed, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			trimmed = filepath.Join(home, strings.TrimLeft(trimmed[1:], `/\`))
		}
	}
	candidate := filepath.Clean(trimmed)
	if !filepath.IsAbs(candidate) {
		if ws == "" {
			return "", fmt.Errorf("access denied: relative path %q needs an absolute path", raw)
		}
		candidate = filepath.Join(ws, candidate)
	}
	// Checked before touching the filesystem: merely resolving a network path makes Windows connect to it.
	if reason := p.windowsPathProblem(candidate, allowNetwork); reason != "" {
		return "", fmt.Errorf("access denied: %q %s", raw, reason)
	}
	resolved, err := resolvePathFollowAncestors(candidate)
	if err != nil {
		return "", fmt.Errorf("access denied: cannot resolve path %q: %w", raw, err)
	}
	if reason := p.windowsPathProblem(resolved, allowNetwork); reason != "" {
		return "", fmt.Errorf("access denied: %q %s", raw, reason) // e.g. a junction pointing to a share
	}
	if reason := p.protectedReason(resolved); reason != "" {
		return "", fmt.Errorf("access denied: %q is %s", raw, reason)
	}
	if !allowNetwork && !p.IsTrusted(resolved) {
		return "", fmt.Errorf("access denied: %q is outside DezuxkStudio's workspace, media, and trusted folders", raw)
	}
	return resolved, nil
}

// windowsPathProblem rejects Windows path forms that bypass the policy's directory checks:
//   - device namespaces (\\?\C:\Windows, \\.\PhysicalDrive0) do not compare equal to C:\Windows;
//   - network shares (\\host\share): touching one sends the user's NTLM credentials to that host, so a
//     prompt-injected path could leak them. Shares are allowed only inside a folder the user trusted;
//   - NTFS alternate data streams (file.bat:payload.txt) would let a ".txt" write land inside another file.

// ValidateDir resolves a directory the agent wants to list or use.
func (p *AccessPolicy) ValidateDir(raw string) (string, error) {
	return p.Resolve(raw)
}

// ValidateRead allows text files (content returned) and media files (shown/inspected).
func (p *AccessPolicy) ValidateRead(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	switch c := p.classifyForPolicy(target); {
	case c == FileClassText || IsMediaClass(c):
		return target, nil
	case c == FileClassForbidden:
		return "", fmt.Errorf("access denied: DezuxkStudio Assistant cannot open source code, scripts, executables, or secret files")
	default:
		return "", fmt.Errorf("access denied: only text (.txt, .md, .json, .srt, .vtt, .csv, …) and image/video/audio files can be opened by the assistant")
	}
}

// ValidateDocumentRead allows extracting plain text from supported rich documents (.docx, .odt, .rtf, .pdf).
func (p *AccessPolicy) ValidateDocumentRead(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(target))
	if readableDocumentExtensions[ext] {
		return target, nil
	}
	if p.classifyForPolicy(target) == FileClassText {
		return "", fmt.Errorf("plain text files use workspace_read_file instead of workspace_read_document (%q)", filepath.Base(target))
	}
	return "", fmt.Errorf("access denied: workspace_read_document only supports .docx, .odt, .rtf, and .pdf files, got %q", filepath.Base(target))
}

// ValidateWrite allows creating/updating text content files inside trusted roots.
func (p *AccessPolicy) ValidateWrite(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	if p.classifyForPolicy(target) != FileClassText {
		return "", fmt.Errorf("access denied: the assistant only writes text content files (.txt, .md, .json, .srt, .vtt, .csv, .xml), got %q", filepath.Base(target))
	}
	return target, nil
}

// ValidateMove allows moving/renaming non-code files; the file type must not change (no .txt → .hta tricks).
func (p *AccessPolicy) ValidateMove(srcRaw, dstRaw string) (string, string, error) {
	src, err := p.Resolve(srcRaw)
	if err != nil {
		return "", "", fmt.Errorf("source %w", err)
	}
	dst, err := p.Resolve(dstRaw)
	if err != nil {
		return "", "", fmt.Errorf("destination %w", err)
	}
	srcClass := p.classifyForPolicy(src)
	if srcClass == FileClassForbidden {
		return "", "", fmt.Errorf("access denied: cannot move source code, executables, or secret files")
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	dstClass := p.classifyForPolicy(dst)
	if dstClass != srcClass || (srcClass == FileClassOther || srcClass == FileClassDocument) &&
		!strings.EqualFold(filepath.Ext(src), filepath.Ext(dst)) {
		return "", "", fmt.Errorf("access denied: moving/renaming must keep the file type (%s → %s)", filepath.Ext(src), filepath.Ext(dst))
	}
	return src, dst, nil
}

// ValidateDelete allows deleting non-code files inside trusted roots.
func (p *AccessPolicy) ValidateDelete(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	targetKey := pathKey(target)
	for _, root := range p.Roots() {
		if pathKey(root) == targetKey {
			return "", fmt.Errorf("access denied: cannot delete a DezuxkStudio root folder")
		}
	}
	p.mu.RLock()
	ws := p.workspaceDir
	projects := p.projectsDir
	media := p.mediaDir
	staging := p.stagingDir
	p.mu.RUnlock()
	if ws != "" {
		if parent := filepath.Dir(ws); parent != ws && pathKey(parent) == targetKey {
			return "", fmt.Errorf("access denied: cannot delete DezuxkStudio workspace root folder")
		}
	}
	if projects != "" && pathKey(projects) == targetKey {
		return "", fmt.Errorf("access denied: cannot delete DezuxkStudio studio projects directory")
	}
	if media != "" && pathKey(media) == targetKey {
		return "", fmt.Errorf("access denied: cannot delete DezuxkStudio media directory")
	}
	if staging != "" && pathKey(staging) == targetKey {
		return "", fmt.Errorf("access denied: cannot delete DezuxkStudio staging directory")
	}
	if p.classifyForPolicy(target) == FileClassForbidden {
		return "", fmt.Errorf("access denied: cannot delete source code, executables, or secret files")
	}
	return target, nil
}

// VisibleInListing reports whether a directory entry should be shown to the agent.

func (p *AccessPolicy) CheckTrustedFile(path string, write bool) error {
	resolved, err := p.Resolve(path)
	if err != nil {
		return apperr.New(apperr.CodeUnauthorized, "access denied: path is outside trusted workspace or inside a protected location")
	}
	if p.protectedReason(resolved) != "" || !p.IsTrusted(resolved) {
		return apperr.New(apperr.CodeUnauthorized, "access denied: path is outside trusted workspace or inside a protected location")
	}

	st, statErr := os.Stat(resolved)
	if statErr == nil {
		if st.IsDir() {
			return nil
		}
	} else if !os.IsNotExist(statErr) {
		return apperr.New(apperr.CodeUnauthorized, "access denied: cannot verify target path")
	}

	base := strings.ToLower(filepath.Base(resolved))
	if write && os.IsNotExist(statErr) && filepath.Ext(base) == "" && !isExplicitlyForbiddenBase(base) {
		// Allow pre-flight directory creation checks (e.g. os.MkdirAll on a new output folder inside a trusted root).
		return nil
	}

	if p.classifyForPolicy(resolved) == FileClassForbidden {
		return apperr.New(apperr.CodeUnauthorized, "access denied: operation on restricted code, executable, database, or secret file is forbidden")
	}
	return nil
}
