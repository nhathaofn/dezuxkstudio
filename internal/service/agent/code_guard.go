package agent

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// This file implements DezuxkStudio's server-side chat output guard.
// File code blocking is enforced by extension via AccessPolicy, while this guard keeps explicit
// programming-language Markdown code fences (e.g. ```python, ```js, ```bash) out of chat responses
// without running line-by-line heuristics that corrupt long scripts, screenplays, tables, or JSON.

// programmingFenceLangs are Markdown fence languages that always denote source code / scripts.
var programmingFenceLangs = map[string]bool{
	"python": true, "py": true, "python3": true, "ipython": true,
	"javascript": true, "js": true, "jsx": true, "mjs": true, "cjs": true, "node": true,
	"typescript": true, "ts": true, "tsx": true,
	"go": true, "golang": true, "rust": true, "rs": true,
	"c": true, "h": true, "cpp": true, "c++": true, "cc": true, "hpp": true, "objc": true, "objective-c": true,
	"cs": true, "csharp": true, "c#": true, "fsharp": true, "f#": true, "vb": true, "vbnet": true, "vba": true, "vbscript": true,
	"java": true, "kotlin": true, "kt": true, "scala": true, "groovy": true, "gradle": true, "clojure": true,
	"swift": true, "dart": true, "php": true, "ruby": true, "rb": true, "perl": true, "pl": true, "lua": true, "r": true,
	"julia": true, "matlab": true, "haskell": true, "hs": true, "elixir": true, "erlang": true, "ocaml": true, "zig": true, "nim": true,
	"sh": true, "bash": true, "zsh": true, "fish": true, "shell": true, "console": true, "shellscript": true,
	"powershell": true, "ps1": true, "pwsh": true, "ps": true, "bat": true, "batch": true, "cmd": true,
	"sql": true, "plsql": true, "tsql": true, "mysql": true, "postgresql": true, "sqlite": true,
	"html": true, "htm": true, "xhtml": true, "css": true, "scss": true, "sass": true, "less": true,
	"vue": true, "svelte": true, "astro": true, "php-template": true,
	"asm": true, "assembly": true, "nasm": true, "wasm": true, "solidity": true, "sol": true,
	"dockerfile": true, "docker": true, "makefile": true, "make": true, "cmake": true, "nix": true,
	"autohotkey": true, "ahk": true, "applescript": true, "regex": true, "graphql": true, "gql": true,
	"yaml": true, "yml": true, "toml": true, "ini": true, "hcl": true, "terraform": true,
}

var (
	reFenceOpen     = regexp.MustCompile("^\\s{0,3}(```+|~~~+)\\s*([A-Za-z0-9_+#.\\-]*)")
	reHTMLCodeTag   = regexp.MustCompile(`(?i)<(?:pre|code)(?:\s[^>]*)?>`)
	reIndentedBlock = regexp.MustCompile(`^(?:    |\t)\s*(.+)$`)
	reStrongCode    = regexp.MustCompile(`^(?:#!/|package\s+[a-z_]\w*|import\s+(?:\(|["']|[a-zA-Z_])|from\s+[a-zA-Z_][\w.]*\s+import\s+|func\s+[A-Za-z_(]|def\s+[A-Za-z_]\w*\s*\(|class\s+[A-Za-z_]\w*\s*[:(\{]|(?:const|let|var)\s+[A-Za-z_$]\w*\s*=|function\s+[A-Za-z_$]\w*\s*\(|#include\s*[<"]|public\s+(?:static\s+)?(?:class|void|int|String)\b|console\.log\s*\(|fmt\.Print(?:ln|f)?\s*\(|(?:SELECT|DELETE\s+FROM|INSERT\s+INTO|UPDATE\s+\w+\s+SET|DROP\s+TABLE)\b)`)
)

func isSafeDataFenceLang(lang string) bool {
	switch lang {
	case "json", "jsonl", "srt", "vtt", "ass", "ssa", "lrc", "sbv", "csv", "tsv", "xml", "fcpxml", "otio":
		return true
	default:
		return false
	}
}

func hasHTMLCodeTag(line string) bool {
	return reHTMLCodeTag.MatchString(line)
}

func isIndentedStrongCodeLine(line string) bool {
	m := reIndentedBlock.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	trimmed := strings.TrimSpace(m[1])
	if trimmed == "" || strings.HasPrefix(trimmed, "(") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return false
	}
	return reStrongCode.MatchString(trimmed) || strings.Contains(trimmed, "=> {")
}

func isBareIndentedLine(line string) bool {
	return reIndentedBlock.MatchString(line)
}

// validateTextCodeWrite blocks writing programming source code into narrative text/markdown files when in assistant mode.
func (s *Service) validateTextCodeWrite(target, content string) string {
	if s.developerMode {
		return ""
	}
	switch strings.ToLower(filepath.Ext(target)) {
	case ".txt", ".md", ".markdown", ".log":
		if ContainsSourceCode(content) {
			return "access denied: DezuxkStudio Assistant cannot write programming source code into files"
		}
	}
	return ""
}

func parseFenceOpen(line string) (mark, lang string, ok bool) {
	m := reFenceOpen.FindStringSubmatchIndex(line)
	if m == nil {
		return "", "", false
	}
	mark = line[m[2]:m[3]]
	lang = strings.ToLower(line[m[4]:m[5]])
	// Backtick code fences cannot contain backticks in their info string (CommonMark),
	// so inline spans like ```foo``` at the start of a line stay allowed.
	if mark[0] == '`' && strings.ContainsRune(line[m[5]:], '`') {
		return "", "", false
	}
	return mark, lang, true
}

func isFenceClose(line, mark string) bool {
	if mark == "" {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < len(mark) {
		return false
	}
	ch := mark[0]
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	return true
}

func partialCouldBeFenceDelimiter(p string) bool {
	if p == "" {
		return false
	}
	spaces := 0
	for spaces < len(p) && spaces < 3 && (p[spaces] == ' ' || p[spaces] == '\t') {
		spaces++
	}
	if spaces == len(p) {
		return true
	}
	rest := p[spaces:]
	marker := rest[0]
	if marker != '`' && marker != '~' {
		return false
	}
	run := 0
	for run < len(rest) && rest[run] == marker {
		run++
	}
	if run == len(rest) {
		return true
	}
	return run >= 3
}

// ContainsSourceCode reports whether text contains programming source code via explicit or nested
// language fences, HTML <pre>/<code> blocks, indented code blocks, or code inside empty/prose fences.
func ContainsSourceCode(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	var safeFenceMark, safeFenceLang string
	indentedStrong := false
	indentedLines := 0
	for _, line := range strings.Split(normalized, "\n") {
		if hasHTMLCodeTag(line) {
			return true
		}
		if safeFenceMark != "" {
			if isFenceClose(line, safeFenceMark) {
				safeFenceMark = ""
				safeFenceLang = ""
				continue
			}
			if !isSafeDataFenceLang(safeFenceLang) {
				if _, innerLang, ok := parseFenceOpen(line); ok && programmingFenceLangs[innerLang] {
					return true
				}
				trimmed := strings.TrimSpace(line)
				if reStrongCode.MatchString(trimmed) || strings.Contains(trimmed, "=> {") {
					return true
				}
			}
			continue
		}
		if mark, lang, ok := parseFenceOpen(line); ok {
			if programmingFenceLangs[lang] {
				return true
			}
			safeFenceMark = mark
			safeFenceLang = lang
			indentedStrong = false
			indentedLines = 0
			continue
		}
		if isBareIndentedLine(line) {
			indentedLines++
			if isIndentedStrongCodeLine(line) {
				indentedStrong = true
			}
			if indentedStrong && indentedLines >= 2 {
				return true
			}
		} else if strings.TrimSpace(line) != "" {
			indentedStrong = false
			indentedLines = 0
		}
	}
	return false
}

// streamCodeFilter incrementally filters a streamed message. Non-fence text and allowed fences
// (bare ``` or data formats such as json/srt/markdown) stream immediately; programming code
// fences, HTML <pre>/<code> blocks, nested code fences, and 4-space indented code blocks are blocked.
type streamCodeFilter struct {
	partial        string // incomplete trailing line
	safeFenceMark  string // active non-programming fence delimiter
	safeFenceLang  string // language tag of active non-programming fence
	indentedLines  int
	indentedStrong bool
	blocked        bool
	released       int // bytes of partial already emitted
}

// push consumes a chunk and returns the text that is safe to release now.
func (f *streamCodeFilter) push(chunk string) (string, bool) {
	if f.blocked {
		return "", true
	}
	data := f.partial + strings.ReplaceAll(chunk, "\r\n", "\n")
	parts := strings.Split(data, "\n")
	f.partial = parts[len(parts)-1]
	var out strings.Builder
	for i, line := range parts[:len(parts)-1] {
		if i == 0 && f.released > 0 {
			if hasHTMLCodeTag(line) {
				f.blocked = true
				f.partial = ""
				f.released = 0
				return out.String(), true
			}
			if f.released < len(line) {
				out.WriteString(line[f.released:])
			}
			out.WriteByte('\n')
			f.released = 0
			continue
		}
		if f.processLine(line, &out) {
			f.partial = ""
			f.released = 0
			return out.String(), true
		}
	}
	f.releasePartial(&out)
	return out.String(), false
}

func partialCouldHoldCode(p, safeFenceMark, safeFenceLang string) bool {
	if partialCouldBeFenceDelimiter(p) {
		return true
	}
	if safeFenceMark != "" && !isSafeDataFenceLang(safeFenceLang) {
		return true
	}
	if strings.HasPrefix(p, "    ") || strings.HasPrefix(p, "\t") {
		return true
	}
	return false
}

func safeChunkBeforeCodeTag(rem string) string {
	lt := strings.IndexByte(rem, '<')
	if lt < 0 {
		return rem
	}
	lower := strings.ToLower(rem[lt:])
	if hasHTMLCodeTag(lower) || strings.HasPrefix("<pre", lower) || strings.HasPrefix("<code", lower) ||
		strings.HasPrefix(lower, "<pre") || strings.HasPrefix(lower, "<code") {
		return rem[:lt]
	}
	return rem
}

func (f *streamCodeFilter) releasePartial(out *strings.Builder) {
	p := f.partial
	if len(p) <= f.released {
		return
	}
	if hasHTMLCodeTag(p) {
		return
	}
	if f.released == 0 && partialCouldHoldCode(p, f.safeFenceMark, f.safeFenceLang) {
		return
	}
	safeRem := safeChunkBeforeCodeTag(p[f.released:])
	if safeRem == "" {
		return
	}
	out.WriteString(safeRem)
	f.released += len(safeRem)
}

// flush emits any buffered non-code text at the end of a turn.
func (f *streamCodeFilter) flush() (string, bool) {
	if f.blocked {
		return "", true
	}
	var out strings.Builder
	if f.released > 0 {
		if hasHTMLCodeTag(f.partial) {
			f.blocked = true
			f.partial = ""
			f.released = 0
			return "", true
		}
		if f.released < len(f.partial) {
			out.WriteString(f.partial[f.released:])
		}
		f.partial = ""
		f.released = 0
		return out.String(), false
	}
	if f.partial != "" {
		line := f.partial
		f.partial = ""
		if f.processLine(line, &out) {
			return out.String(), true
		}
		return strings.TrimSuffix(out.String(), "\n"), false
	}
	return "", false
}

// processLine handles one complete line; returns true when the message must be blocked.
func (f *streamCodeFilter) processLine(line string, out *strings.Builder) bool {
	if hasHTMLCodeTag(line) {
		f.blocked = true
		return true
	}
	if f.safeFenceMark != "" {
		if isFenceClose(line, f.safeFenceMark) {
			f.safeFenceMark = ""
			f.safeFenceLang = ""
			out.WriteString(line)
			out.WriteByte('\n')
			return false
		}
		if !isSafeDataFenceLang(f.safeFenceLang) {
			if _, innerLang, ok := parseFenceOpen(line); ok && programmingFenceLangs[innerLang] {
				f.blocked = true
				return true
			}
			trimmed := strings.TrimSpace(line)
			if reStrongCode.MatchString(trimmed) || strings.Contains(trimmed, "=> {") {
				f.blocked = true
				return true
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
		return false
	}
	if mark, lang, ok := parseFenceOpen(line); ok {
		if programmingFenceLangs[lang] {
			f.blocked = true
			return true
		}
		f.safeFenceMark = mark
		f.safeFenceLang = lang
		f.indentedLines = 0
		f.indentedStrong = false
		out.WriteString(line)
		out.WriteByte('\n')
		return false
	}
	if isBareIndentedLine(line) {
		f.indentedLines++
		if isIndentedStrongCodeLine(line) {
			f.indentedStrong = true
		}
		if f.indentedStrong && f.indentedLines >= 2 {
			f.blocked = true
			return true
		}
	} else if strings.TrimSpace(line) != "" {
		f.indentedLines = 0
		f.indentedStrong = false
	}
	out.WriteString(line)
	out.WriteByte('\n')
	return false
}

// turnOutputGuard filters both the visible answer and the visible reasoning of a single agent turn.
type turnOutputGuard struct {
	mu         sync.Mutex
	messageID  string
	notice     string
	text       streamCodeFilter
	thought    streamCodeFilter
	textOut    strings.Builder
	thoughtOut strings.Builder
	noticed    bool
	finalized  bool
	bypass     bool
}

func newTurnOutputGuard(messageID string) *turnOutputGuard {
	return &turnOutputGuard{messageID: messageID, notice: noticeMarker(noticeCodeBlocked)}
}

// disable marks the guard as transparent (used in opt-in Developer Mode).
func (g *turnOutputGuard) disable() {
	g.mu.Lock()
	g.bypass = true
	g.mu.Unlock()
}

// filterText returns the safe portion of an answer chunk (with a one-time refusal notice once blocked).
func (g *turnOutputGuard) filterText(chunk string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalized {
		return ""
	}
	if g.bypass {
		g.textOut.WriteString(chunk)
		return chunk
	}
	safe, blocked := g.text.push(chunk)
	return g.recordText(safe, blocked)
}

// filterThought returns the safe portion of a reasoning chunk; reasoning is silently cut once code appears.
func (g *turnOutputGuard) filterThought(chunk string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalized {
		return ""
	}
	if g.bypass {
		g.thoughtOut.WriteString(chunk)
		return chunk
	}
	safe, _ := g.thought.push(chunk)
	g.thoughtOut.WriteString(safe)
	return safe
}

// finalize flushes held text; it returns the remaining safe answer/thought to emit, and is idempotent.
func (g *turnOutputGuard) finalize() (string, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalized {
		return "", ""
	}
	g.finalized = true
	if g.bypass {
		return "", ""
	}
	safe, blocked := g.text.flush()
	textTail := g.recordText(safe, blocked)
	thoughtTail, _ := g.thought.flush()
	g.thoughtOut.WriteString(thoughtTail)
	return textTail, thoughtTail
}

func (g *turnOutputGuard) recordText(safe string, blocked bool) string {
	if blocked && !g.noticed {
		g.noticed = true
		if safe != "" && !strings.HasSuffix(safe, "\n") {
			safe += "\n"
		}
		if safe != "" || g.textOut.Len() > 0 {
			safe += "\n"
		}
		safe += g.notice
	}
	g.textOut.WriteString(safe)
	return safe
}

func (g *turnOutputGuard) blocked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.text.blocked
}

func (g *turnOutputGuard) sanitizedText() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.textOut.String()
}

func (g *turnOutputGuard) sanitizedThought() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.thoughtOut.String()
}
