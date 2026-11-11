package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// AccessPolicy decides which files DezuxkStudio's agent may touch.
//
// DezuxkStudio is a desktop app, so the agent works on the user's whole machine by default. The policy only
// blocks what must never be touched:
//   - secrets and credentials (SSH/cloud keys, .env, password stores, browser profiles, CLI agent configs);
//   - source code and executables (the agent is not a programming assistant);
//   - operating-system directories and DezuxkStudio's own internal data (database, isolated agent profiles, logs).
//
// Paths are additionally classified as *trusted* (DezuxkStudio workspace, media dir, folders the user granted, and
// in the future project folders): there the agent works freely. Elsewhere on the machine reading and creating files
// is free, but destructive actions (overwrite, delete) require the user's approval in the UI.
type AccessPolicy struct {
	mu           sync.RWMutex
	dataDir      string
	workspaceDir string
	mediaDir     string
	profilesDir  string
	trustedDirs  []string
	protected    []string // absolute directories that are always off-limits
}

// FileClass groups file extensions by what the agent may do with them.
type FileClass string

const (
	FileClassText      FileClass = "text"
	FileClassImage     FileClass = "image"
	FileClassVideo     FileClass = "video"
	FileClassAudio     FileClass = "audio"
	FileClassDocument  FileClass = "document" // readable by the user's apps; the agent may organize but not read them
	FileClassForbidden FileClass = "forbidden"
	FileClassOther     FileClass = "other"
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".jsonl": true,
	".srt": true, ".vtt": true, ".ass": true, ".ssa": true, ".lrc": true, ".sbv": true,
	".csv": true, ".tsv": true, ".xml": true, ".log": true,
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".bmp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true, ".avif": true,
}

var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true,
	".wmv": true, ".flv": true, ".mpg": true, ".mpeg": true, ".ts": false, ".3gp": true,
}

var audioExtensions = map[string]bool{
	".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".opus": true, ".wma": true,
}

var documentExtensions = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".odt": true, ".ods": true, ".odp": true, ".rtf": true, ".psd": true, ".ai": true, ".svg": false,
	".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true,
	".ttf": true, ".otf": true, ".woff": true, ".woff2": true,
	".prproj": true, ".aep": true, ".drp": true, ".fcpxml": true, ".kdenlive": true, ".cube": true,
}

// forbiddenExtensions are source code, scripts, executables, and secret containers.
var forbiddenExtensions = map[string]bool{
	// Source code.
	".go": true, ".mod": true, ".sum": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".pyw": true, ".ipynb": true, ".rb": true, ".php": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
	".hpp": true, ".cc": true, ".cs": true, ".java": true, ".kt": true, ".kts": true, ".swift": true, ".dart": true,
	".lua": true, ".pl": true, ".r": true, ".scala": true, ".groovy": true, ".gradle": true, ".vue": true, ".svelte": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true, ".less": true, ".svg": true, ".sql": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".properties": true,
	".tf": true, ".nix": true, ".asm": true, ".s": true, ".ahk": true, ".au3": true,
	// Scripts and executables.
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true, ".psm1": true, ".psd1": true, ".ps1xml": true,
	".psc1": true, ".bat": true, ".cmd": true, ".vbs": true, ".vbe": true, ".vb": true, ".js_": true, ".jse": true, ".wsf": true,
	".wsh": true, ".hta": true, ".exe": true, ".dll": true, ".sys": true, ".drv": true, ".ocx": true, ".msi": true, ".msp": true,
	".msix": true, ".appx": true, ".appxbundle": true, ".com": true, ".scr": true, ".pif": true, ".cpl": true, ".msc": true,
	".lnk": true, ".url": true, ".scf": true, ".inf": true, ".reg": true, ".jar": true, ".class": true, ".apk": true,
	".app": true, ".dmg": true, ".pkg": true, ".deb": true, ".rpm": true, ".so": true, ".dylib": true, ".bin": true,
	".application": true, ".gadget": true, ".workflow": true, ".command": true,
	// Secrets and databases.
	".env": true, ".pem": true, ".key": true, ".crt": true, ".cer": true, ".p12": true, ".pfx": true, ".jks": true,
	".keystore": true, ".kdbx": true, ".kdb": true, ".gpg": true, ".asc": true, ".ovpn": true, ".rdp": true,
	".db": true, ".sqlite": true, ".sqlite3": true, ".mdb": true, ".accdb": true,
}

// forbiddenFileNames are exact (lower-case) file names that hold secrets or developer configuration.
var forbiddenFileNames = map[string]bool{
	".env": true, ".netrc": true, "_netrc": true, ".npmrc": true, ".yarnrc": true, ".pypirc": true, ".git-credentials": true,
	".gitconfig": true, ".bashrc": true, ".zshrc": true, ".profile": true, ".bash_profile": true, ".bash_history": true,
	".zsh_history": true, "known_hosts": true, "authorized_keys": true, "id_rsa": true, "id_dsa": true, "id_ecdsa": true,
	"id_ed25519": true, "credentials": true, "credentials.json": true, "client_secret.json": true, "service-account.json": true,
	"auth.json": true, "token.json": true, "secrets.json": true, "login data": true, "cookies": true, "web data": true,
	"package.json": true, "package-lock.json": true, "tsconfig.json": true, "jsconfig.json": true, "composer.json": true,
	"wails.json": true, "deno.json": true, "go.work": true, "dockerfile": true, "makefile": true, "cmakelists.txt": true,
	"requirements.txt": true, "pipfile": true, "gemfile": true, "cargo.lock": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"desktop.ini": true, "ntuser.dat": true, "pagefile.sys": true, "hiberfil.sys": true, "swapfile.sys": true,
}

// forbiddenSegmentNames are directory names that are never entered (case-insensitive).
var forbiddenSegmentNames = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true,
	".docker": true, ".password-store": true, ".terraform": true, ".vscode": true, ".idea": true, ".vs": true,
	".claude": true, ".codex": true, ".gemini": true, ".agents": true, ".cursor": true, ".continue": true,
	"node_modules": true, "agent-profiles": true, "antigravity-cli": true, "gcloud": true,
	"$recycle.bin": true, "system volume information": true, "recovery": true, "windowsapps": true,
}

// NewAccessPolicy constructs the policy. allowedDirs are user-granted trusted folders.
func NewAccessPolicy(dataDir, workspaceDir, mediaDir, profilesDir string, allowedDirs []string) *AccessPolicy {
	p := &AccessPolicy{
		dataDir:      normalizeAbsPath(dataDir),
		workspaceDir: normalizeAbsPath(workspaceDir),
		mediaDir:     normalizeAbsPath(mediaDir),
		profilesDir:  normalizeAbsPath(profilesDir),
	}
	p.protected = p.buildProtectedDirs()
	p.SetAllowedDirs(allowedDirs)
	return p
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
		for _, sub := range []string{"backups", "diagnostics", "logs", "tools", "agent-profiles"} {
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

// Roots returns the trusted roots (workspace, media, user-granted folders).
func (p *AccessPolicy) Roots() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []string
	if p.workspaceDir != "" {
		out = append(out, p.workspaceDir)
	}
	if p.mediaDir != "" && pathKey(p.mediaDir) != pathKey(p.workspaceDir) {
		out = append(out, p.mediaDir)
	}
	return append(out, p.trustedDirs...)
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

// Resolve turns a user/agent supplied path into a clean absolute path and rejects protected locations.
// Relative paths are resolved against the agent workspace.
func (p *AccessPolicy) Resolve(raw string) (string, error) {
	return p.resolve(raw, false)
}

func (p *AccessPolicy) resolve(raw string, allowNetwork bool) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), `"'`)
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("access denied: invalid path %q", raw)
	}
	if trimmed == "" {
		if p.workspaceDir == "" {
			return "", fmt.Errorf("access denied: path is required")
		}
		trimmed = p.workspaceDir
	}
	if strings.HasPrefix(trimmed, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			trimmed = filepath.Join(home, strings.TrimLeft(trimmed[1:], `/\`))
		}
	}
	candidate := filepath.Clean(trimmed)
	if !filepath.IsAbs(candidate) {
		if p.workspaceDir == "" {
			return "", fmt.Errorf("access denied: relative path %q needs an absolute path", raw)
		}
		candidate = filepath.Join(p.workspaceDir, candidate)
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
	return resolved, nil
}

// windowsPathProblem rejects Windows path forms that bypass the policy's directory checks:
//   - device namespaces (\\?\C:\Windows, \\.\PhysicalDrive0) do not compare equal to C:\Windows;
//   - network shares (\\host\share): touching one sends the user's NTLM credentials to that host, so a
//     prompt-injected path could leak them. Shares are allowed only inside a folder the user trusted;
//   - NTFS alternate data streams (file.bat:payload.txt) would let a ".txt" write land inside another file.
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
	if hasForbiddenSegment(resolved) {
		return "inside a protected folder (credentials, developer or system data)"
	}
	if p.dataDir != "" && isWithinDir(resolved, p.dataDir) {
		base := strings.ToLower(filepath.Base(resolved))
		if strings.HasPrefix(base, "app.db") || strings.HasSuffix(base, ".db") {
			return "DezuxkStudio's internal database"
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

// ClassifyFile returns the class of a file by name.
func ClassifyFile(path string) FileClass {
	base := strings.ToLower(filepath.Base(path))
	if forbiddenFileNames[base] || base == ".env" || strings.HasPrefix(base, ".env.") ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa") ||
		strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, ".eslintrc") || strings.HasPrefix(base, ".prettierrc") {
		return FileClassForbidden
	}
	ext := strings.ToLower(filepath.Ext(base))
	switch {
	case forbiddenExtensions[ext]:
		return FileClassForbidden
	case textExtensions[ext]:
		return FileClassText
	case imageExtensions[ext]:
		return FileClassImage
	case videoExtensions[ext]:
		return FileClassVideo
	case audioExtensions[ext]:
		return FileClassAudio
	case documentExtensions[ext]:
		return FileClassDocument
	case ext == "":
		return FileClassForbidden // extension-less files are typically secrets, binaries, or scripts
	}
	return FileClassOther
}

// IsMediaClass reports whether the class is an image, video, or audio file.
func IsMediaClass(c FileClass) bool {
	return c == FileClassImage || c == FileClassVideo || c == FileClassAudio
}

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
	switch c := ClassifyFile(target); {
	case c == FileClassText || IsMediaClass(c):
		return target, nil
	case c == FileClassForbidden:
		return "", fmt.Errorf("access denied: DezuxkStudio Assistant cannot open source code, scripts, executables, or secret files")
	default:
		return "", fmt.Errorf("access denied: only text (.txt, .md, .json, .srt, .vtt, .csv, …) and image/video/audio files can be opened by the assistant")
	}
}

// ValidateWrite allows creating/updating text content files anywhere that is not protected.
func (p *AccessPolicy) ValidateWrite(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	if ClassifyFile(target) != FileClassText {
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
	srcClass := ClassifyFile(src)
	if srcClass == FileClassForbidden {
		return "", "", fmt.Errorf("access denied: cannot move source code, executables, or secret files")
	}
	if st, err := os.Stat(dst); err == nil && st.IsDir() {
		dst = filepath.Join(dst, filepath.Base(src))
	}
	dstClass := ClassifyFile(dst)
	if dstClass != srcClass || (srcClass == FileClassOther || srcClass == FileClassDocument) &&
		!strings.EqualFold(filepath.Ext(src), filepath.Ext(dst)) {
		return "", "", fmt.Errorf("access denied: moving/renaming must keep the file type (%s → %s)", filepath.Ext(src), filepath.Ext(dst))
	}
	return src, dst, nil
}

// ValidateDelete allows deleting non-code files; callers require approval outside trusted roots.
func (p *AccessPolicy) ValidateDelete(raw string) (string, error) {
	target, err := p.Resolve(raw)
	if err != nil {
		return "", err
	}
	for _, root := range p.Roots() {
		if pathKey(root) == pathKey(target) {
			return "", fmt.Errorf("access denied: cannot delete a DezuxkStudio root folder")
		}
	}
	if ClassifyFile(target) == FileClassForbidden {
		return "", fmt.Errorf("access denied: cannot delete source code, executables, or secret files")
	}
	return target, nil
}

// VisibleInListing reports whether a directory entry should be shown to the agent.
func (p *AccessPolicy) VisibleInListing(full string, isDir bool) bool {
	if p.protectedReason(full) != "" {
		return false
	}
	return isDir || ClassifyFile(full) != FileClassForbidden
}

func hasForbiddenSegment(path string) bool {
	for _, seg := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if forbiddenSegmentNames[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func isVolumeRoot(p string) bool {
	clean := filepath.Clean(p)
	return filepath.Dir(clean) == clean
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
