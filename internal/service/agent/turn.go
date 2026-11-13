package agent

import (
	"context"
	"strings"
	"sync"

	domainagent "app/internal/domain/agent"
)

// maxRecordedTools bounds the audit list of one reply.
const maxRecordedTools = 24

// turn is one chat turn: everything that lives exactly as long as the turn is kept here, so every way a
// turn can end (finished, cancelled, timed out, disconnected, process exited) goes through end() once and
// releases the same state. Service.turn points at the running turn; nil means the agent is idle.
type turn struct {
	id             string
	conversationID string
	providerID     domainagent.ProviderID
	connToken      int64 // connection the turn started on (Service.connSeq)
	touch          func()
	cancel         context.CancelFunc
	guard          *turnOutputGuard // every answer/reasoning chunk passes through it; it is also what is stored

	mu        sync.Mutex
	tools     []string          // tools the reply called, in order, once each
	acpTitles map[string]string // ACP toolCallId -> title shown when the tool started
	endOnce   sync.Once
}

func newTurn(id, conversationID string, providerID domainagent.ProviderID, connToken int64, touch func(), cancel context.CancelFunc) *turn {
	return &turn{
		id:             id,
		conversationID: conversationID,
		providerID:     providerID,
		connToken:      connToken,
		touch:          touch,
		cancel:         cancel,
		guard:          newTurnOutputGuard(id),
		acpTitles:      map[string]string{},
	}
}

// noteTool records that the reply called tool.
func (t *turn) noteTool(tool string) {
	if tool == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, known := range t.tools {
		if known == tool {
			return
		}
	}
	if len(t.tools) < maxRecordedTools {
		t.tools = append(t.tools, tool)
	}
}

// toolList returns the recorded tools joined for storage.
func (t *turn) toolList() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.tools, ", ")
}

// trackACPTool returns the title to show for an ACP tool event: the title recorded when the tool started
// (keyed by toolCallId), so start and end events name the same tool row. Finished tools are forgotten.
func (t *turn) trackACPTool(upd SessionUpdate) string {
	if upd.ToolCallID == "" {
		return upd.Tool
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	title, known := t.acpTitles[upd.ToolCallID]
	if !known && upd.Tool != "" {
		title = upd.Tool
		t.acpTitles[upd.ToolCallID] = title
	}
	if upd.Status == toolStatusCompleted || upd.Status == toolStatusFailed {
		delete(t.acpTitles, upd.ToolCallID)
	}
	if title == "" {
		return upd.Tool
	}
	return title
}
