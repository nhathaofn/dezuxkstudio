package agent

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// This file implements DezuxkStudio's server-side output guard. The system prompt asks the model to refuse
// programming work, but prompts can be jailbroken; the guard is the hard layer that keeps source code from
// ever reaching the UI, chat history, or files written by the agent, regardless of what the model produced.

// codeRunThreshold is how many code-like lines (in one run, ignoring neutral lines) count as a program.
// One or two stray lines (e.g. a single ffmpeg command mentioned in passing) are tolerated.
const codeRunThreshold = 3

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
	reFenceOpen = regexp.MustCompile("^\\s{0,3}(```+|~~~+)\\s*([A-Za-z0-9_+#.\\-]*)")

	codeLinePatterns = []*regexp.Regexp{
		// Declarations / definitions (case-sensitive keywords so Vietnamese/English prose rarely matches).
		regexp.MustCompile(`^\s*(async\s+)?def\s+\w+\s*\(`),
		regexp.MustCompile(`^\s*class\s+\w+\s*[(:{<]`),
		regexp.MustCompile(`^\s*class\s+\w+\s*(extends|implements)\b`),
		regexp.MustCompile(`^\s*(export\s+(default\s+)?)?(async\s+)?function\*?\s*\w*\s*\(`),
		regexp.MustCompile(`^\s*func\s+(\(\w+\s+\*?\w+\)\s*)?\w+\s*\(`),
		regexp.MustCompile(`^\s*(pub\s+)?(async\s+)?fn\s+\w+\s*[(<]`),
		regexp.MustCompile(`^\s*(pub\s+)?(struct|enum|trait|impl|interface|type)\s+\w+.*[{=]\s*$`),
		regexp.MustCompile(`^\s*(public|private|protected|internal|static|final|abstract|override|virtual)\s+[\w<>\[\],\s]+\(`),
		regexp.MustCompile(`^\s*(public|private|protected|internal)\s+(static\s+)?(class|interface|enum|record|struct)\b`),
		regexp.MustCompile(`^\s*(const|let|var|val)\s+[\w${}\[\],\s]+\s*[:=]`),
		regexp.MustCompile(`^\s*(package|namespace)\s+[\w.]+\s*;?\s*\{?\s*$`),
		regexp.MustCompile(`^\s*#\s*(include|define|ifdef|ifndef|endif|pragma)\b`),
		regexp.MustCompile(`^\s*using\s+(namespace\s+)?[\w.]+\s*;\s*$`),
		regexp.MustCompile(`^\s*(import\s+[\w.]+(\s+as\s+\w+)?|from\s+[\w.]+\s+import\s+.+)\s*;?\s*$`),
		regexp.MustCompile(`^\s*import\s+.*\s+from\s+['"]`),
		regexp.MustCompile(`^\s*import\s+(\(|"[\w./-]+")`),
		regexp.MustCompile(`^\s*(module\.exports|exports\.\w+)\s*=`),
		regexp.MustCompile(`require\(\s*['"][\w@./-]+['"]\s*\)`),
		regexp.MustCompile(`^\s*@\w+(\(.*\))?\s*$`), // decorators / annotations

		// Control flow.
		regexp.MustCompile(`^\s*(if|elif|else\s+if|for|foreach|while|switch|catch|with)\s*\(.*\)\s*\{?\s*$`),
		regexp.MustCompile(`^\s*(if|elif|for|while|with)\s+[^\n]*:\s*$`),
		regexp.MustCompile(`^\s*(else|try|finally|do)\s*[:{]\s*$`),
		regexp.MustCompile(`^\s*\}\s*(else|catch|finally|while)\b`),
		regexp.MustCompile(`^\s*(return|yield|throw|raise)(\s+[\w\s.()\[\]{}"'+\-*/%<>=!&|,:]*)?;?\s*$`),
		regexp.MustCompile(`^\s*(break|continue|pass)\s*;?\s*$`),
		regexp.MustCompile(`^\s*case\s+.+:\s*$`),

		// Statements and expressions typical of source code.
		regexp.MustCompile(`;\s*(//.*)?$`),
		regexp.MustCompile(`\)\s*\{\s*$`),
		regexp.MustCompile(`\)\s*=>|=>\s*\{\s*$`),
		regexp.MustCompile(`\w\s*:=\s*`),
		regexp.MustCompile(`^\s*[\w.$\[\]]+\s*(\+|-|\*|/|\|\||&&|\?\?)?=\s*(["'(\[{]|new\s|await\s|lambda\b|function\b|[\w.$]+\().*$`),
		regexp.MustCompile(`^\s*[\w$]+(\.\w+)*\(.*\)\s*;?\s*$`), // print("x"), console.log(x)
		regexp.MustCompile(`^\s*(//|/\*|\*/|#!)`),
		regexp.MustCompile(`^\s*<(script|style|\?php|html|head|body|template|svg|iframe|form)\b`),
		regexp.MustCompile(`^\s*</?(div|span|button|input|section|main|nav|ul|li|table|tr|td)\b[^>]*>`),
		regexp.MustCompile(`(?i)^\s*(select\s+.+\s+from\s+\w|insert\s+into\s+\w|update\s+\w+\s+set\s|delete\s+from\s+\w|create\s+(table|index|view|database|procedure|function)\b|drop\s+(table|database)\b|alter\s+table\b)`),

		// Shell / terminal commands.
		regexp.MustCompile(`^\s*(\$|PS [^>]*>|[A-Z]:\\[^>]*>)\s*\S+`),
		regexp.MustCompile(`^\s*(sudo|apt|apt-get|yum|dnf|brew|choco|winget|scoop|npm|npx|pnpm|yarn|bun|pip|pip3|pipx|poetry|conda|cargo|rustc|gcc|g\+\+|clang|make|cmake|javac|dotnet|node|deno|python|python3|ruby|gem|php|composer|perl|java|mvn|gradle|git|docker|kubectl|helm|terraform|curl|wget|chmod|chown|mkdir|rm|mv|cp|cat|echo|export|setx|cd|ls|grep|sed|awk|find|xargs|ssh|scp|nc|netcat|nmap|powershell|pwsh|cmd|reg|schtasks|taskkill|invoke-webrequest|iwr|iex)(\.exe)?\s+[-\w./\\$"'~=:@%*]`),
		regexp.MustCompile(`^\s*(Get|Set|New|Remove|Rename|Copy|Move|Clear|Invoke|Start|Stop|Restart|Write|Read|Add|Import|Export|Test|Out|Select|Where|ForEach|Convert|ConvertTo|ConvertFrom|Format|Measure|Sort|Group|Join|Split|Wait|Enable|Disable|Register|Unregister|Install|Uninstall|Update|Resolve|Push|Pop|Compare|Expand|Compress|Send|Receive|Connect|Disconnect|Enter|Exit|Find|Save|Show|Use|Mount)-[A-Z]\w+`),
		regexp.MustCompile(`\$_(\.|\s|$)|\$PSItem\b|\$env:\w+`),
		regexp.MustCompile(`^\s*(for|while|until|if|elif|select|case)\s+.*;\s*(do|then|in)\s*$`),
		regexp.MustCompile(`^\s*(done|fi|esac|then|do|endlocal|setlocal|goto\s+\w+|:\w+)\s*$`),
		regexp.MustCompile(`(?i)^\s*@?echo\s+(off|on)\s*$`),
		regexp.MustCompile(`^\s*\$\w+\s*=`),
		regexp.MustCompile(`\|\s*(grep|awk|sed|xargs|sort|head|tail|iex|Out-File|Select-Object|Where-Object|ForEach-Object)\b`),
	}

	// neutralLinePatterns are lines that neither prove nor disprove code (braces, indented continuations).
	neutralLinePattern = regexp.MustCompile(`^\s*[{}()\[\];,]+\s*$`)
)

// isCodeLikeLine reports whether a single line has strong source-code / shell signatures.
func isCodeLikeLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Subtitle timing lines (00:00:01,000 --> 00:00:02,000) and Markdown list/heading prose are not code.
	if strings.Contains(trimmed, "-->") && strings.ContainsAny(trimmed, "0123456789") && !strings.Contains(trimmed, "<!--") {
		return false
	}
	for _, re := range codeLinePatterns {
		if re.MatchString(line) {
			return !looksLikeProse(trimmed)
		}
	}
	return false
}

// looksLikeProse filters out false positives: natural-language sentences that happen to match a pattern
// (e.g. "Nhấn Start rồi chọn file video (mp4)." or a Vietnamese sentence ending with ';').
func looksLikeProse(trimmed string) bool {
	words := strings.Fields(trimmed)
	if len(words) < 6 {
		return false
	}
	letters, nonASCIILetters, symbols := 0, 0, 0
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r):
			letters++
			if r > unicode.MaxASCII {
				nonASCIILetters++
			}
		case strings.ContainsRune("{}[]();=<>$&|*/\\`", r):
			symbols++
		}
	}
	if letters == 0 {
		return false
	}
	// Vietnamese prose (many diacritic letters) with few code symbols is prose.
	if nonASCIILetters*6 >= letters && symbols <= 2 {
		return true
	}
	// Long sentences with almost no code punctuation are prose.
	return len(words) >= 10 && symbols <= 1 && strings.HasSuffix(trimmed, ".")
}

// isNeutralLine reports lines that keep a code run alive without counting toward it.
func isNeutralLine(line string) bool {
	if neutralLinePattern.MatchString(line) {
		return true
	}
	// Deeply indented lines continue a code block (Python bodies, nested statements).
	if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
		t := strings.TrimSpace(line)
		return t != "" && !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ")
	}
	return false
}

// fencedBlockIsCode decides whether a complete fenced block (```lang ... ```) contains source code.
func fencedBlockIsCode(lang string, body []string) bool {
	lang = strings.ToLower(strings.TrimSpace(lang))
	nonBlank := 0
	for _, l := range body {
		if strings.TrimSpace(l) != "" {
			nonBlank++
		}
	}
	if nonBlank == 0 {
		return false
	}
	if programmingFenceLangs[lang] {
		return true
	}
	joined := strings.Join(body, "\n")
	if lang == "json" || lang == "jsonc" || lang == "json5" {
		if json.Valid([]byte(joined)) {
			return jsonContainsCode(joined)
		}
	}
	return linesContainCode(body)
}

// linesContainCode applies the run-based heuristic to plain lines.
func linesContainCode(lines []string) bool {
	run := 0
	for _, l := range lines {
		switch {
		case strings.TrimSpace(l) == "":
			continue
		case isCodeLikeLine(l):
			run++
			if run >= codeRunThreshold {
				return true
			}
		case isNeutralLine(l) && run > 0:
			continue
		default:
			run = 0
		}
	}
	return false
}

// jsonContainsCode checks string values inside a JSON document for embedded programs
// (e.g. {"script": "import os\nos.system(...)\n..."}).
func jsonContainsCode(raw string) bool {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(n any) bool {
		switch t := n.(type) {
		case string:
			return strings.Contains(t, "\n") && ContainsSourceCode(t)
		case []any:
			for _, e := range t {
				if walk(e) {
					return true
				}
			}
		case map[string]any:
			for _, e := range t {
				if walk(e) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

// ContainsSourceCode reports whether text contains a program, script, or terminal command sequence.
// Valid JSON data, subtitles, prompts, scripts for videos (screenplays), and prose are allowed.
func ContainsSourceCode(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return jsonContainsCode(trimmed)
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var outside []string
	for i := 0; i < len(lines); i++ {
		m := reFenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			outside = append(outside, lines[i])
			continue
		}
		fence, lang := m[1], m[2]
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), fence[:3]) {
				break
			}
			body = append(body, lines[j])
		}
		if fencedBlockIsCode(lang, body) {
			return true
		}
		i = j
		outside = append(outside, "") // a fence breaks a code run
	}
	return linesContainCode(outside)
}

// streamCodeFilter incrementally filters a streamed message. Text is released line-by-line; code-like lines
// and fenced blocks are held back until they are proven harmless, so blocked code never reaches the UI.
type streamCodeFilter struct {
	partial   string   // incomplete trailing line
	pending   []string // held lines of a possible code run
	run       int      // code-like lines in the pending run
	inFence   bool
	fenceMark string
	fenceLang string
	fenceBuf  []string // fence opening line + body
	blocked   bool
	released  int // bytes of the current partial line already streamed out as prose
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
			f.finishReleasedLine(line, &out)
			out.WriteByte('\n')
			continue
		}
		if f.processLine(line, &out) {
			return out.String(), true
		}
	}
	f.releaseProsePartial(&out)
	return out.String(), false
}

// releaseProsePartial streams an unfinished line immediately when it is clearly prose, so short answers and
// long paragraphs appear word-by-word instead of only after a newline arrives.
func (f *streamCodeFilter) releaseProsePartial(out *strings.Builder) {
	p := f.partial
	if f.inFence || len(f.pending) > 0 || len(p) <= f.released {
		return
	}
	if f.released == 0 {
		if !partialLooksLikeProse(p) {
			return
		}
	}
	out.WriteString(p[f.released:])
	f.released = len(p)
}

// finishReleasedLine completes a line whose beginning was already streamed as prose.
// It still counts toward a code run so the following lines are judged correctly.
func (f *streamCodeFilter) finishReleasedLine(line string, out *strings.Builder) {
	if f.released < len(line) {
		out.WriteString(line[f.released:])
	}
	f.released = 0
	if isCodeLikeLine(line) {
		f.run = 1
	} else {
		f.run = 0
	}
}

// partialLooksLikeProse reports whether an unfinished line is safe to show before it is complete.
func partialLooksLikeProse(p string) bool {
	t := strings.TrimSpace(p)
	if t == "" || p != strings.TrimLeft(p, " \t") || strings.HasPrefix(t, "`") || strings.HasPrefix(t, "~") ||
		strings.HasPrefix(t, "<") || strings.HasPrefix(t, "$") || strings.HasPrefix(t, "#!") || isCodeLikeLine(p) {
		return false
	}
	if strings.ContainsAny(t, "(){}[];=<>$|\\`") {
		return false
	}
	nonASCII := false
	for _, r := range t {
		if r > unicode.MaxASCII && unicode.IsLetter(r) {
			nonASCII = true
			break
		}
	}
	return nonASCII || len(strings.Fields(t)) >= 4
}

// flush releases whatever is still held at the end of the message.
func (f *streamCodeFilter) flush() (string, bool) {
	if f.blocked {
		return "", true
	}
	var out strings.Builder
	if f.released > 0 {
		line := f.partial
		f.partial = ""
		f.finishReleasedLine(line, &out)
		return out.String(), false
	}
	if f.partial != "" {
		line := f.partial
		f.partial = ""
		if f.processLine(line, &out) {
			return out.String(), true
		}
		// processLine always appends '\n'; the final line had none.
		s := strings.TrimSuffix(out.String(), "\n")
		out.Reset()
		out.WriteString(s)
		if !f.inFence && len(f.pending) == 0 {
			return out.String(), false
		}
	}
	if f.inFence {
		// Unterminated fence: judge what we have.
		body := f.fenceBuf[1:]
		if fencedBlockIsCode(f.fenceLang, body) {
			f.blocked = true
			return out.String(), true
		}
		out.WriteString(strings.Join(f.fenceBuf, "\n"))
		f.inFence = false
		f.fenceBuf = nil
	}
	if len(f.pending) > 0 {
		out.WriteString(strings.Join(f.pending, "\n"))
		f.pending = nil
		f.run = 0
	}
	return out.String(), false
}

// processLine handles one complete line; returns true when the message must be blocked.
func (f *streamCodeFilter) processLine(line string, out *strings.Builder) bool {
	if f.inFence {
		if strings.HasPrefix(strings.TrimSpace(line), f.fenceMark[:3]) {
			body := f.fenceBuf[1:]
			if fencedBlockIsCode(f.fenceLang, body) {
				f.blocked = true
				return true
			}
			for _, l := range f.fenceBuf {
				out.WriteString(l)
				out.WriteByte('\n')
			}
			out.WriteString(line)
			out.WriteByte('\n')
			f.inFence = false
			f.fenceBuf = nil
			return false
		}
		f.fenceBuf = append(f.fenceBuf, line)
		return false
	}
	if m := reFenceOpen.FindStringSubmatch(line); m != nil {
		f.releasePending(out)
		f.inFence = true
		f.fenceMark = m[1]
		f.fenceLang = m[2]
		f.fenceBuf = []string{line}
		return false
	}
	switch {
	case strings.TrimSpace(line) == "":
		if len(f.pending) > 0 {
			f.pending = append(f.pending, line)
			return false
		}
	case isCodeLikeLine(line):
		f.pending = append(f.pending, line)
		f.run++
		if f.run >= codeRunThreshold {
			f.blocked = true
			return true
		}
		return false
	case isNeutralLine(line) && len(f.pending) > 0:
		f.pending = append(f.pending, line)
		return false
	default:
		f.releasePending(out)
	}
	out.WriteString(line)
	out.WriteByte('\n')
	return false
}

func (f *streamCodeFilter) releasePending(out *strings.Builder) {
	for _, l := range f.pending {
		out.WriteString(l)
		out.WriteByte('\n')
	}
	f.pending = nil
	f.run = 0
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
}

func newTurnOutputGuard(messageID string) *turnOutputGuard {
	return &turnOutputGuard{messageID: messageID, notice: noticeMarker(noticeCodeBlocked)}
}

// filterText returns the safe portion of an answer chunk (with a one-time refusal notice once blocked).
func (g *turnOutputGuard) filterText(chunk string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalized {
		return ""
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
