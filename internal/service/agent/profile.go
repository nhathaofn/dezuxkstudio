package agent

import (
	_ "embed"
	"strings"
)

//go:embed profile/agent.md
var embeddedAgentProfile string

// SystemInstructions returns the non-overridable default role and guardrail prompt for DezuxkStudio's agent.
func SystemInstructions() string {
	return strings.TrimSpace(embeddedAgentProfile)
}
