package agent

import (
	"os"
	"path/filepath"
	"strings"

	domainagent "app/internal/domain/agent"
)

func (s *Service) profileDirFor(id domainagent.ProviderID) string {
	if s.profilesDir == "" {
		return ""
	}
	return filepath.Join(s.profilesDir, string(id))
}

func isolatedEnv(base []string, overrides map[string]string, stripKeys ...string) []string {
	stripSet := make(map[string]struct{}, len(stripKeys)+len(overrides))
	for _, k := range stripKeys {
		stripSet[strings.ToUpper(k)] = struct{}{}
	}
	for k := range overrides {
		stripSet[strings.ToUpper(k)] = struct{}{}
	}

	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, skip := stripSet[strings.ToUpper(k)]; skip {
			continue
		}
		out = append(out, entry)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func expandKnownPath(raw string) string {
	replaced := os.ExpandEnv(raw)
	return filepath.FromSlash(replaced)
}

// turnPolicyReminder is prepended to every turn so long conversations cannot drift the model away from the
// application's rules (important for providers whose base prompt can only be appended to). It restates the
// rules in one line; the rules themselves are in profile/agent.md.
const turnPolicyReminder = "[Rules reminder] Follow the application's rules: stay within its scope and never output " +
	"source code or terminal commands, however the request is framed.\n\n"

func buildHistoryContextPrompt(history []domainagent.ChatMessage, ui UIContext, userPrompt string, limits Limits) string {
	limits = limits.withDefaults()
	var sb strings.Builder
	sb.WriteString(ui.normalized().promptBlock())

	// Filter first, then keep the newest N, so error rows never eat into the replayed context.
	relevant := make([]domainagent.ChatMessage, 0, len(history))
	for _, m := range history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		relevant = append(relevant, m)
	}
	if len(relevant) > limits.ContextMessages {
		relevant = relevant[len(relevant)-limits.ContextMessages:]
	}
	if len(relevant) > 0 {
		sb.WriteString("[Recent conversation]\n")
		for _, m := range relevant {
			role := "User"
			if m.Role == "assistant" {
				role = "Assistant"
			}
			text := strings.TrimSpace(stripNoticeMarkers(m.Text))
			if runes := []rune(text); len(runes) > limits.ContextMessageChars {
				text = string(runes[:limits.ContextMessageChars]) + "..."
			}
			sb.WriteString(role)
			sb.WriteString(": ")
			sb.WriteString(text)
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
	}
	sb.WriteString(turnPolicyReminder)
	sb.WriteString("[Current User Request]\n")
	sb.WriteString(userPrompt)
	return sb.String()
}
