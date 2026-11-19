package agent

import (
	"path/filepath"
	"strings"
)

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
	".csv": true, ".tsv": true, ".xml": true, ".fcpxml": true, ".otio": true, ".log": true,
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
	".prproj": true, ".aep": true, ".drp": true, ".kdenlive": true, ".cube": true,
}

// readableDocumentExtensions are document formats whose plain text can be extracted via workspace_read_document.
var readableDocumentExtensions = map[string]bool{
	".docx": true,
	".odt":  true,
	".rtf":  true,
	".pdf":  true,
}

// developerSourceExtensions are source/config/script extensions permitted ONLY when Developer Mode is enabled.
// Executables (.exe, .dll, ...) and secret/database containers (.pem, .key, .env, .db, ...) stay forbidden.
var developerSourceExtensions = map[string]bool{
	".go": true, ".mod": true, ".sum": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".pyw": true, ".ipynb": true, ".rb": true, ".php": true, ".rs": true, ".c": true, ".h": true, ".cpp": true,
	".hpp": true, ".cc": true, ".cs": true, ".java": true, ".kt": true, ".kts": true, ".swift": true, ".dart": true,
	".lua": true, ".pl": true, ".r": true, ".scala": true, ".groovy": true, ".gradle": true, ".vue": true, ".svelte": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true, ".less": true, ".svg": true, ".sql": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".properties": true,
	".tf": true, ".nix": true, ".asm": true, ".s": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true, ".psm1": true, ".psd1": true, ".bat": true, ".cmd": true,
}

// developerProjectFileNames are build/project config file names unlocked ONLY in Developer Mode.
var developerProjectFileNames = map[string]bool{
	"package.json": true, "package-lock.json": true, "tsconfig.json": true, "jsconfig.json": true, "composer.json": true,
	"wails.json": true, "deno.json": true, "go.work": true, "dockerfile": true, "makefile": true, "cmakelists.txt": true,
	"requirements.txt": true, "pipfile": true, "gemfile": true, "cargo.lock": true, "yarn.lock": true, "pnpm-lock.yaml": true,
}

// developerConfigSegmentNames are hidden repo/editor folders allowed in Developer Mode (excluding secret/profile dirs).
var developerConfigSegmentNames = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".vscode": true, ".idea": true, ".vs": true,
	".claude": true, ".codex": true, ".gemini": true, ".agents": true, ".cursor": true, ".continue": true,
	"node_modules": true,
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
	".keystore": true, ".kdbx": true, ".kdb": true, ".gpg": true, ".asc": true, ".enc": true, ".ovpn": true, ".rdp": true,
	".db": true, ".db-wal": true, ".db-shm": true,
	".sqlite": true, ".sqlite-wal": true, ".sqlite-shm": true,
	".sqlite3": true, ".sqlite3-wal": true, ".sqlite3-shm": true,
	".mdb": true, ".accdb": true,
}

// forbiddenFileNames are exact (lower-case) file names that hold secrets or developer configuration.
var forbiddenFileNames = map[string]bool{
	".env": true, ".netrc": true, "_netrc": true, ".npmrc": true, ".yarnrc": true, ".pypirc": true, ".git-credentials": true,
	".gitconfig": true, ".bashrc": true, ".zshrc": true, ".profile": true, ".bash_profile": true, ".bash_history": true,
	".zsh_history": true, "known_hosts": true, "authorized_keys": true, "id_rsa": true, "id_dsa": true, "id_ecdsa": true,
	"id_ed25519": true, "credentials": true, "credentials.json": true, "client_secret.json": true, "service-account.json": true,
	"auth.json": true, "token.json": true, "secrets.json": true, "secrets.enc": true, "master.key": true, "login data": true, "cookies": true, "web data": true,
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

// dosDeviceNames contains Windows legacy DOS reserved device names (case-insensitive).
var dosDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	"lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
	"conin$": true, "conout$": true,
}

// isDOSDeviceSegment reports whether a path segment matches a Windows DOS reserved device name,

// ClassifyFile returns the class of a file by name.
func ClassifyFile(path string) FileClass {
	rawBase := strings.ToLower(filepath.Base(path))
	if rawBase != "." && rawBase != ".." && strings.TrimRight(rawBase, ". ") != rawBase {
		return FileClassForbidden
	}
	base := strings.TrimRight(rawBase, ". ")
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

// Classify returns the effective FileClass taking Developer Mode into account.
func (p *AccessPolicy) Classify(path string) FileClass { return p.classifyForPolicy(path) }

// classifyForPolicy returns the effective FileClass taking Developer Mode into account.
// Assistant mode uses the strict ClassifyFile rules unchanged; Developer Mode additionally allows
// source/config text files while keeping executables and credential files forbidden.
func (p *AccessPolicy) classifyForPolicy(path string) FileClass {
	c := ClassifyFile(path)
	if !p.isDeveloperMode() || c != FileClassForbidden {
		return c
	}
	base := strings.ToLower(filepath.Base(path))
	// Secret files always stay forbidden, even in developer mode.
	if forbiddenFileNames[base] && !developerProjectFileNames[base] {
		return FileClassForbidden
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") ||
		strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa") {
		return FileClassForbidden
	}
	if developerProjectFileNames[base] ||
		strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, ".eslintrc") || strings.HasPrefix(base, ".prettierrc") {
		return FileClassText
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" || developerSourceExtensions[ext] {
		return FileClassText
	}
	return FileClassForbidden
}
