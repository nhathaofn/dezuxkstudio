package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reCustomizationSlug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type parsedFrontmatter struct {
	Fields map[string]any
	Body   string
}

func validateCustomizationSlug(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || !reCustomizationSlug.MatchString(trimmed) {
		return fmt.Errorf("invalid name %q: must be lowercase alphanumeric and hyphens (1-64 chars)", name)
	}
	return nil
}

// parseMarkdownFrontmatter extracts YAML-style key/value & list metadata delimited by --- fences.
func parseMarkdownFrontmatter(raw []byte) (parsedFrontmatter, error) {
	text := strings.TrimPrefix(string(raw), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return parsedFrontmatter{
			Fields: map[string]any{},
			Body:   strings.TrimSpace(text),
		}, nil
	}

	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return parsedFrontmatter{
			Fields: map[string]any{},
			Body:   strings.TrimSpace(text),
		}, nil
	}

	fields, err := parseFrontmatterBlock(lines[1:endIdx])
	if err != nil {
		return parsedFrontmatter{}, err
	}
	body := strings.TrimSpace(strings.Join(lines[endIdx+1:], "\n"))
	return parsedFrontmatter{Fields: fields, Body: body}, nil
}

func parseFrontmatterBlock(lines []string) (map[string]any, error) {
	out := make(map[string]any)
	var currentListKey string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			if currentListKey == "" {
				continue
			}
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if item == "" {
				continue
			}
			list, _ := out[currentListKey].([]string)
			out[currentListKey] = append(list, unquoteYAMLScalar(stripInlineYAMLComment(item)))
			continue
		}

		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("frontmatter: invalid line %q", trimmed)
		}
		key := strings.TrimSpace(line[:colon])
		rawVal := strings.TrimSpace(line[colon+1:])
		currentListKey = ""

		if rawVal == "" || strings.HasPrefix(rawVal, "#") {
			currentListKey = key
			out[key] = []string{}
			continue
		}

		val := stripInlineYAMLComment(rawVal)
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			out[key] = parseInlineYAMLList(val)
			continue
		}
		switch strings.ToLower(val) {
		case "true":
			out[key] = true
		case "false":
			out[key] = false
		default:
			out[key] = unquoteYAMLScalar(val)
		}
	}
	return out, nil
}

func parseInlineYAMLList(val string) []string {
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if inner == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if quote != 0 {
			if ch == '\\' && quote == '"' && i+1 < len(inner) {
				cur.WriteByte(ch)
				i++
				cur.WriteByte(inner[i])
				continue
			}
			if ch == quote {
				quote = 0
			}
			cur.WriteByte(ch)
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' {
			if item := unquoteYAMLScalar(strings.TrimSpace(cur.String())); item != "" {
				out = append(out, item)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if item := unquoteYAMLScalar(strings.TrimSpace(cur.String())); item != "" {
		out = append(out, item)
	}
	return out
}

func stripInlineYAMLComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == '\\' && quote == '"' && i+1 < len(s) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimSpace(s[:i])
		}
	}
	return strings.TrimSpace(s)
}

func unquoteYAMLScalar(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func fmString(fields map[string]any, key string) string {
	v, ok := fields[key]
	if !ok {
		return ""
	}
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s)
	case bool:
		if s {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func fmBool(fields map[string]any, key string, def bool) bool {
	v, ok := fields[key]
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "yes", "1":
			return true
		case "false", "no", "0":
			return false
		}
	}
	return def
}

func fmStringSlice(fields map[string]any, key string) []string {
	v, ok := fields[key]
	if !ok {
		return nil
	}
	switch list := v.(type) {
	case []string:
		out := make([]string, 0, len(list))
		seen := make(map[string]bool, len(list))
		for _, it := range list {
			trimmed := strings.TrimSpace(it)
			if trimmed == "" || seen[trimmed] {
				continue
			}
			seen[trimmed] = true
			out = append(out, trimmed)
		}
		return out
	case string:
		if strings.TrimSpace(list) == "" {
			return nil
		}
		return []string{strings.TrimSpace(list)}
	default:
		return nil
	}
}

func yamlQuoteScalar(s string) string {
	clean := strings.Join(strings.Fields(strings.ReplaceAll(s, "\r\n", " ")), " ")
	if clean == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(clean, ":#[]{}&*!|>'\"%@`") ||
		strings.HasPrefix(clean, "-") ||
		strings.EqualFold(clean, "true") ||
		strings.EqualFold(clean, "false") ||
		strings.EqualFold(clean, "null")
	if !needsQuote {
		return clean
	}
	return strconv.Quote(clean)
}

func renderSkillMarkdown(name, description string, userInvocable, enabled bool, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", yamlQuoteScalar(description))
	fmt.Fprintf(&sb, "user-invocable: %t\n", userInvocable)
	fmt.Fprintf(&sb, "enabled: %t\n", enabled)
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")))
	sb.WriteByte('\n')
	return sb.String()
}

func renderSubagentMarkdown(name, description string, skills, tools []string, provider, model string, enabled bool, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", name)
	fmt.Fprintf(&sb, "description: %s\n", yamlQuoteScalar(description))
	if len(skills) > 0 {
		sb.WriteString("skills:\n")
		for _, sk := range skills {
			fmt.Fprintf(&sb, "  - %s\n", sk)
		}
	}
	if len(tools) > 0 {
		sb.WriteString("tools:\n")
		for _, tl := range tools {
			fmt.Fprintf(&sb, "  - %s\n", tl)
		}
	}
	if strings.TrimSpace(provider) != "" {
		fmt.Fprintf(&sb, "provider: %s\n", strings.TrimSpace(provider))
	}
	if strings.TrimSpace(model) != "" {
		fmt.Fprintf(&sb, "model: %s\n", yamlQuoteScalar(model))
	}
	fmt.Fprintf(&sb, "enabled: %t\n", enabled)
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")))
	sb.WriteByte('\n')
	return sb.String()
}
