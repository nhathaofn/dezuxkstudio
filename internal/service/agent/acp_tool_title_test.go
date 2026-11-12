package agent

import "testing"

func TestTrackACPToolKeepsStartTitle(t *testing.T) {
	s := &Service{}
	if got := s.trackACPTool(SessionUpdate{Kind: "tool_call", ToolCallID: "c1", Tool: "dezuxk/workflow_run_node", Status: "in_progress"}); got != "dezuxk/workflow_run_node" {
		t.Fatalf("start title %q", got)
	}
	// untitled and differently titled updates resolve to the start title
	if got := s.trackACPTool(SessionUpdate{Kind: "tool_call_update", ToolCallID: "c1"}); got != "dezuxk/workflow_run_node" {
		t.Fatalf("untitled update %q", got)
	}
	if got := s.trackACPTool(SessionUpdate{Kind: "tool_call_update", ToolCallID: "c1", Tool: "Run node", Status: toolStatusCompleted}); got != "dezuxk/workflow_run_node" {
		t.Fatalf("final update %q", got)
	}
	if len(s.acpToolTitles) != 0 {
		t.Fatal("finished tool must be forgotten")
	}
	// no id: title passes through
	if got := s.trackACPTool(SessionUpdate{Kind: "tool_call", Tool: "x"}); got != "x" {
		t.Fatalf("no id %q", got)
	}
}
