package agent

import (
	"slices"
	"strings"
	"testing"

	domainagent "app/internal/domain/agent"
)

func TestClaudePartialMessagesStreamWithoutDuplicates(t *testing.T) {
	var events []domainagent.ChatEvent
	acc := &claudeStreamAccumulator{providerID: domainagent.ProviderClaude, messageID: "m", emit: func(e domainagent.ChatEvent) {
		events = append(events, e)
	}}
	lines := []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Plan"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Xin "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"chào"}}}`,
		// The complete message repeats the streamed text/thinking and adds a tool call.
		`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"Plan"},{"type":"text","text":"Xin chào"},{"type":"tool_use","id":"t1","name":"mcp__dezuxk__studio_list_capabilities"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_2"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Xong."}}}`,
		`{"type":"assistant","message":{"id":"msg_2","content":[{"type":"text","text":"Xong."}]}}`,
		`{"type":"result","result":"Xong."}`,
	}
	for _, l := range lines {
		acc.handleLine(l)
	}
	var got []string
	for _, e := range events {
		got = append(got, string(e.Type)+":"+e.Text)
	}
	want := []string{
		"thought:Plan", "delta:Xin ", "delta:chào",
		"tool_call:studio_list_capabilities", "tool_result:ok",
		"delta:\n\nXong.",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events =\n%q\nwant\n%q", got, want)
	}
	if acc.assistantOutput() != "Xin chào\n\nXong." || acc.thoughtOutput() != "Plan" {
		t.Fatalf("accumulated text=%q thought=%q", acc.assistantOutput(), acc.thoughtOutput())
	}
	if !strings.Contains(strings.Join(buildClaudeExecArgs("cfg.json", "x"), " "), "--include-partial-messages") {
		t.Fatal("claude must be asked for partial messages")
	}
}
