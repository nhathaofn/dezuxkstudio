package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"app/internal/fsutil"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxPagedFileBytes       = 10 << 20 // 10 MiB max file scanned/edited via paged tools
	maxRecursiveListLimit   = 1000
	defaultSearchMaxResults = 50
	maxSearchResultsCap     = 200
	maxSearchSnippetRunes   = 300
	defaultDocReadChars     = 32000
	maxDocReadChars         = 100000
)

// validateJSONWrite validates JSON syntax for .json files before touching disk or prompting for
// approval, surfacing clear syntax errors and a curly-quote hint when typographic quotes break parsing.
func validateJSONWrite(path, content string) string {
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		msg := fmt.Sprintf("invalid JSON for %s: %v", filepath.Base(path), err)
		if strings.ContainsAny(content, "\u201c\u201d\u201e\u201f") {
			msg += "; hint: typographic curly quotes (\u201c \u201d) were found; use standard ASCII double quotes (\") for JSON keys and string delimiters, and escape inner quotes with \\\""
		}
		return msg
	}
	return ""
}

type workspaceListEntry struct {
	Name  string    `json:"name"`
	Path  string    `json:"path"`
	IsDir bool      `json:"is_dir"`
	Class FileClass `json:"class,omitempty"`
	Size  int64     `json:"size,omitempty"`
}

func (s *Service) handleWorkspaceListFiles(in listFilesToolInput) (*mcp.CallToolResult, any, error) {
	target, err := s.policy.ValidateDir(in.Dir)
	if err != nil {
		return mcpError(err.Error())
	}
	limit := in.MaxEntries
	if limit <= 0 {
		limit = maxListEntries
	}
	if limit > maxRecursiveListLimit {
		limit = maxRecursiveListLimit
	}

	out := make([]workspaceListEntry, 0, min(limit, 64))
	counts := map[FileClass]int{}
	truncated := false

	if !in.Recursive {
		entries, err := os.ReadDir(target)
		if err != nil {
			return mcpError(err.Error())
		}
		for _, e := range entries {
			full := filepath.Join(target, e.Name())
			if !s.policy.VisibleInListing(full, e.IsDir()) {
				continue
			}
			it := workspaceListEntry{Name: e.Name(), Path: full, IsDir: e.IsDir()}
			if !e.IsDir() {
				it.Class = s.policy.Classify(full)
				counts[it.Class]++
				if info, statErr := e.Info(); statErr == nil {
					it.Size = info.Size()
				}
			}
			if len(out) >= limit {
				truncated = true
				continue
			}
			out = append(out, it)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	} else {
		walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if path == target {
				return nil
			}
			if !s.policy.VisibleInListing(path, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(target, path)
			if relErr != nil {
				rel = d.Name()
			}
			rel = filepath.ToSlash(rel)
			it := workspaceListEntry{Name: rel, Path: path, IsDir: d.IsDir()}
			if !d.IsDir() {
				it.Class = s.policy.Classify(path)
				counts[it.Class]++
				if info, statErr := d.Info(); statErr == nil {
					it.Size = info.Size()
				}
			}
			if len(out) >= limit {
				truncated = true
				return fs.SkipAll
			}
			out = append(out, it)
			return nil
		})
		if walkErr != nil {
			return mcpError(walkErr.Error())
		}
	}

	return mcpJSON(map[string]any{
		"dir":       target,
		"trusted":   s.policy.IsTrusted(target),
		"recursive": in.Recursive,
		"counts":    counts,
		"truncated": truncated,
		"entries":   out,
	})
}

func (s *Service) handleWorkspaceReadTextSlice(target string, info os.FileInfo, startLine, endLine int) (*mcp.CallToolResult, any, error) {
	if startLine == 0 && endLine == 0 {
		if info.Size() > maxWorkspaceReadBytes {
			return mcpError(fmt.Sprintf("file exceeds maximum readable size (%d bytes); pass start_line and end_line to read in chunks", maxWorkspaceReadBytes))
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return mcpError(err.Error())
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return mcpError("file is not a UTF-8 text file")
		}
		content := string(b)
		return mcpJSON(map[string]any{"path": target, "size": len(b), "content": content})
	}

	if info.Size() > maxPagedFileBytes {
		return mcpError(fmt.Sprintf("file exceeds maximum paged readable size (%d bytes)", maxPagedFileBytes))
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return mcpError(err.Error())
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return mcpError("file is not a UTF-8 text file")
	}

	normalized := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	totalLines := len(lines)
	start := max(startLine, 1)
	if start > totalLines {
		return mcpJSON(map[string]any{
			"path":        target,
			"size":        len(b),
			"total_lines": totalLines,
			"start_line":  start,
			"end_line":    totalLines,
			"has_more":    false,
			"content":     "",
		})
	}
	end := endLine
	if end <= 0 || end > totalLines {
		end = totalLines
	}
	if end < start {
		return mcpError("end_line must be >= start_line")
	}

	var sb strings.Builder
	lastIncluded := start - 1
	hasMore := false
	for ln := start; ln <= end; ln++ {
		line := lines[ln-1]
		addLen := len(line)
		if lastIncluded >= start {
			addLen++
		}
		if sb.Len() > 0 && sb.Len()+addLen > maxWorkspaceReadBytes {
			hasMore = true
			break
		}
		if sb.Len() == 0 && len(line) > maxWorkspaceReadBytes {
			line = truncateUTF8Bytes(line, maxWorkspaceReadBytes)
			sb.WriteString(line)
			lastIncluded = ln
			hasMore = ln < totalLines
			break
		}
		if lastIncluded >= start {
			sb.WriteByte('\n')
		}
		sb.WriteString(line)
		lastIncluded = ln
	}
	if lastIncluded < totalLines {
		hasMore = true
	}

	chunk := sb.String()
	nextStart := 0
	if hasMore {
		nextStart = lastIncluded + 1
	}
	return mcpJSON(map[string]any{
		"path":            target,
		"size":            len(b),
		"total_lines":     totalLines,
		"start_line":      start,
		"end_line":        lastIncluded,
		"has_more":        hasMore,
		"next_start_line": nextStart,
		"content":         chunk,
	})
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func (s *Service) handleWorkspaceEditFile(ctx context.Context, in editFileToolInput) (*mcp.CallToolResult, any, error) {
	target, err := s.policy.ValidateWrite(in.Path)
	if err != nil {
		return mcpError(err.Error())
	}

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = "replace"
	}

	info, statErr := os.Stat(target)
	if statErr == nil && info.IsDir() {
		return mcpError("target path is a folder")
	}

	var updated string
	switch mode {
	case "append":
		var existing string
		if statErr == nil {
			if info.Size() > maxPagedFileBytes {
				return mcpError(fmt.Sprintf("file exceeds maximum editable size (%d bytes)", maxPagedFileBytes))
			}
			b, readErr := os.ReadFile(target)
			if readErr != nil {
				return mcpError(readErr.Error())
			}
			if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
				return mcpError("file is not a UTF-8 text file")
			}
			existing = string(b)
		} else if !os.IsNotExist(statErr) {
			return mcpError(statErr.Error())
		}
		updated = existing + in.Content
	case "replace":
		if statErr != nil {
			return mcpError(statErr.Error())
		}
		if info.Size() > maxPagedFileBytes {
			return mcpError(fmt.Sprintf("file exceeds maximum editable size (%d bytes)", maxPagedFileBytes))
		}
		b, readErr := os.ReadFile(target)
		if readErr != nil {
			return mcpError(readErr.Error())
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return mcpError("file is not a UTF-8 text file")
		}
		existing := string(b)
		switch {
		case in.OldText != "":
			count := strings.Count(existing, in.OldText)
			if count == 0 {
				return mcpError("old_text not found in target file")
			}
			if count > 1 && !in.ReplaceAll {
				return mcpError(fmt.Sprintf("old_text matched %d locations; provide more context or set replace_all=true", count))
			}
			if in.ReplaceAll {
				updated = strings.ReplaceAll(existing, in.OldText, in.Content)
			} else {
				updated = strings.Replace(existing, in.OldText, in.Content, 1)
			}
		case in.StartLine > 0:
			eol := "\n"
			if strings.Contains(existing, "\r\n") {
				eol = "\r\n"
			}
			lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
			start := in.StartLine
			end := in.EndLine
			if end <= 0 {
				end = start
			}
			if start > len(lines) || end < start || end > len(lines) {
				return mcpError(fmt.Sprintf("line range %d-%d is out of bounds (file has %d lines)", start, end, len(lines)))
			}
			repLines := strings.Split(strings.ReplaceAll(in.Content, "\r\n", "\n"), "\n")
			nextLines := make([]string, 0, len(lines)-(end-start+1)+len(repLines))
			nextLines = append(nextLines, lines[:start-1]...)
			nextLines = append(nextLines, repLines...)
			nextLines = append(nextLines, lines[end:]...)
			updated = strings.Join(nextLines, eol)
		default:
			return mcpError("replace mode requires old_text or start_line")
		}
	default:
		return mcpError(fmt.Sprintf("unsupported edit mode %q: expected \"replace\" or \"append\"", in.Mode))
	}

	if errMsg := validateJSONWrite(target, updated); errMsg != "" {
		return mcpError(errMsg)
	}

	totalLines := strings.Count(strings.ReplaceAll(updated, "\r\n", "\n"), "\n") + 1
	applyEdit := func() (any, error) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := fsutil.WriteFileAtomic(target, []byte(updated), 0o644); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "path": target, "mode": mode, "bytes": len(updated), "total_lines": totalLines}, nil
	}

	if statErr == nil && !s.policy.IsTrusted(target) {
		return s.needsApproval(ctx, "workspace_edit_file", "Edit "+filepath.Base(target),
			fmt.Sprintf("Edit %s using mode %q (%d bytes resulting).", target, mode, len(updated)),
			func(context.Context) (any, error) { return applyEdit() })
	}
	res, err := applyEdit()
	if err != nil {
		return mcpError(err.Error())
	}
	return mcpJSON(res)
}

func (s *Service) handleWorkspaceSearchText(in searchTextToolInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return mcpError("query cannot be empty")
	}
	target, err := s.policy.ValidateDir(in.Dir)
	if err != nil {
		return mcpError(err.Error())
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = defaultSearchMaxResults
	}
	if limit > maxSearchResultsCap {
		limit = maxSearchResultsCap
	}

	needle := in.Query
	if !in.CaseSensitive {
		needle = strings.ToLower(needle)
	}

	type match struct {
		Path    string `json:"path"`
		RelPath string `json:"rel_path"`
		Line    int    `json:"line"`
		Text    string `json:"text"`
	}
	var matches []match
	truncated := false
	filesSearched := 0

	walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == target {
			return nil
		}
		if !s.policy.VisibleInListing(path, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if s.policy.Classify(path) != FileClassText {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > maxPagedFileBytes {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return nil
		}
		content := string(b)
		filesSearched++
		rel, relErr := filepath.Rel(target, path)
		if relErr != nil {
			rel = d.Name()
		}
		rel = filepath.ToSlash(rel)

		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		for idx, line := range lines {
			haystack := line
			if !in.CaseSensitive {
				haystack = strings.ToLower(haystack)
			}
			if !strings.Contains(haystack, needle) {
				continue
			}
			snippet := strings.TrimSpace(line)
			if r := []rune(snippet); len(r) > maxSearchSnippetRunes {
				snippet = string(r[:maxSearchSnippetRunes]) + "..."
			}
			matches = append(matches, match{
				Path:    path,
				RelPath: rel,
				Line:    idx + 1,
				Text:    snippet,
			})
			if len(matches) >= limit {
				truncated = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return mcpError(walkErr.Error())
	}

	return mcpJSON(map[string]any{
		"dir":            target,
		"query":          in.Query,
		"files_searched": filesSearched,
		"truncated":      truncated,
		"matches":        matches,
	})
}

func (s *Service) handleWorkspaceReadDocument(in readDocumentToolInput) (*mcp.CallToolResult, any, error) {
	target, err := s.policy.ValidateDocumentRead(in.Path)
	if err != nil {
		return mcpError(err.Error())
	}
	format, fullText, sourceTruncated, err := extractDocumentTextBounded(target, maxExtractedDocumentRunes)
	if err != nil {
		return mcpError(err.Error())
	}

	runes := []rune(fullText)
	totalChars := len(runes)
	offset := max(in.OffsetChars, 0)
	limit := in.LimitChars
	if limit <= 0 {
		limit = defaultDocReadChars
	}
	if limit > maxDocReadChars {
		limit = maxDocReadChars
	}
	if offset >= totalChars {
		return mcpJSON(map[string]any{
			"path":             target,
			"format":           format,
			"offset_chars":     offset,
			"limit_chars":      limit,
			"total_chars":      totalChars,
			"source_truncated": sourceTruncated,
			"has_more":         false,
			"next_offset":      totalChars,
			"content":          "",
		})
	}
	end := min(offset+limit, totalChars)
	hasMore := end < totalChars
	return mcpJSON(map[string]any{
		"path":             target,
		"format":           format,
		"offset_chars":     offset,
		"limit_chars":      limit,
		"total_chars":      totalChars,
		"source_truncated": sourceTruncated,
		"has_more":         hasMore,
		"next_offset":      end,
		"content":          string(runes[offset:end]),
	})
}
