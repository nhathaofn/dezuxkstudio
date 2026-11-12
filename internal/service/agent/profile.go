package agent

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"sort"
	"strings"

	domainagent "app/internal/domain/agent"
)

// The system prompt is assembled in layers, most stable first, so providers can reuse (prompt-cache) the
// unchanged prefix across calls:
//
//  1. policy  — profile/policy.md: the application's fixed rules (scope, constraints, safety, honesty).
//     It names no tools, features or languages.
//  2. <application> — runtime facts: product name, version, platform, interface language.
//  3. <features> — the live list of workflow features, generated from their engine.Spec, so the model
//     knows what the app can do without spending a tool call (and quota) to find out.
//
// How to use each tool lives in the tool's own description; per-turn data (screen context) is sent with
// the user's message, and only when it changed (see screenContextForTurn).

//go:embed profile/policy.md
var embeddedPolicy string

// SystemInstructions returns the policy layer alone ({{app}} replaced). It is the fallback prompt when
// runtime context is unavailable and the source of truth for the rules.
func SystemInstructions() string {
	// Normalize line endings: a Windows checkout (core.autocrlf) would otherwise embed CRLF.
	policy := strings.ReplaceAll(strings.TrimSpace(embeddedPolicy), "\r\n", "\n")
	return strings.ReplaceAll(policy, "{{app}}", clientName)
}

// systemPrompt assembles all layers for a new session of provider id.
func (s *Service) systemPrompt(ctx context.Context, id domainagent.ProviderID) string {
	var sb strings.Builder
	sb.WriteString(SystemInstructions())

	sb.WriteString("\n\n<application>\n")
	fmt.Fprintf(&sb, "name: %s\nversion: %s\nplatform: %s\n", clientName, s.appVersion, runtime.GOOS)
	if s.preferences != nil {
		if prefs, err := s.preferences.Get(ctx); err == nil && prefs.Language != "" {
			fmt.Fprintf(&sb, "interface_language: %s\n", prefs.Language)
		}
	}
	sb.WriteString("</application>")

	if block := s.featuresBlock(ctx); block != "" {
		sb.WriteString("\n\n")
		sb.WriteString(block)
	}
	if hint := s.driver(id).toolCallHint(); hint != "" {
		sb.WriteString("\n\n")
		sb.WriteString(s.toolsBlock(hint))
	}
	return sb.String()
}

// toolsBlock lists the application's tools, generated from the MCP server's registrations. It is only
// sent to providers whose model does not see tool definitions natively.
func (s *Service) toolsBlock(hint string) string {
	var sb strings.Builder
	sb.WriteString("<tools>\nThe application's tools. " + hint + "\n")
	for _, d := range s.toolDocs() {
		fmt.Fprintf(&sb, "- %s: %s\n", d.Name, d.Description)
	}
	sb.WriteString("</tools>")
	return sb.String()
}

// featuresBlock lists the registered workflow features, one line each, sorted for a stable prompt.
func (s *Service) featuresBlock(ctx context.Context) string {
	if s.workflow == nil {
		return ""
	}
	specs := s.workflow.Specs(ctx)
	if len(specs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(specs))
	for _, sp := range specs {
		var inputs, settings []string
		for _, p := range sp.Inputs {
			inputs = append(inputs, fmt.Sprintf("%s:%s", p.Name, p.Type))
		}
		for _, p := range sp.Params {
			settings = append(settings, p.Name)
		}
		line := fmt.Sprintf("- %s (%s): %s", sp.Type, sp.Label, oneLine(sp.Description))
		if len(inputs) > 0 {
			line += " | inputs: " + strings.Join(inputs, ", ")
		}
		if len(settings) > 0 {
			line += " | settings: " + strings.Join(settings, ", ")
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return "<features>\nRun with workflow_run_node; full settings, options and defaults via workflow_list_specs.\n" +
		strings.Join(lines, "\n") + "\n</features>"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// screenContextForTurn returns the screen context to send with this turn, or an empty context when the
// session already received the same one. A resumed session keeps earlier messages, so repeating an
// unchanged block on every turn only costs tokens.
func (s *Service) screenContextForTurn(sessionKey string, ui UIContext) UIContext {
	block := ui.normalized().promptBlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionKey != "" && s.sentScreen[sessionKey] == block {
		return UIContext{}
	}
	return ui
}

// rememberScreenContext records what a session has seen, after a turn ran in it.
func (s *Service) rememberScreenContext(sessionKey string, ui UIContext) {
	if sessionKey == "" {
		return
	}
	s.mu.Lock()
	s.sentScreen[sessionKey] = ui.normalized().promptBlock()
	s.mu.Unlock()
}
