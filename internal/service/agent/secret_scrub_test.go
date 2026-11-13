package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	domainagent "app/internal/domain/agent"
	"app/internal/engine"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	scrubKey     = "sk-live-VERY-SECRET-123456"
	scrubShort   = "abc123"
	scrubPlain   = "plain-visible-value"
	scrubNodeTyp = "tool.keysave"
)

func scrubSpec() engine.Spec {
	return engine.Spec{
		Type: scrubNodeTyp, Label: "Save key", Description: "Saves a key.",
		Params: []engine.Param{
			{Name: "api_key", Label: "API key", Kind: engine.ParamText, Secret: true},
			{Name: "short_key", Label: "Short", Kind: engine.ParamText, Secret: true},
			{Name: "name", Label: "Name", Kind: engine.ParamText},
		},
	}
}

func scrubFixture(t *testing.T) (*fakeRepo, *mcp.ClientSession) {
	t.Helper()
	base := t.TempDir()
	repo := newFakeRepo()
	dirs := []string{"ws", "media", "profiles"}
	for i := range dirs {
		dirs[i] = filepath.Join(base, "data", dirs[i])
	}
	svc, err := New(Options{
		Repo: repo, DataDir: filepath.Join(base, "data"), WorkspaceDir: dirs[0], MediaDir: dirs[1],
		ProfilesDir: dirs[2], Logger: slog.New(slog.DiscardHandler),
		Workflow: &fakeWorkflow{specs: []engine.Spec{scrubSpec()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	repo.messages = []domainagent.ChatMessage{
		{ID: "1", Role: "user", Text: "my key is " + scrubKey + " and short " + scrubShort + " and " + scrubPlain},
		{ID: "2", Role: "tool", Tool: `{"input":{"api_key":"` + scrubKey + `","short_key":"` + scrubShort + `","name":"` + scrubPlain + `"}}`},
		{ID: "3", Role: "assistant", Thought: "using " + scrubKey},
	}
	return repo, cs
}

func assertScrubbed(t *testing.T, repo *fakeRepo) {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, m := range repo.messages {
		all := m.Text + m.Thought + m.Tool
		if strings.Contains(all, scrubKey) {
			t.Errorf("message %s still holds the secret: %q", m.ID, all)
		}
	}
	if !strings.Contains(repo.messages[0].Text, "[secret saved]") || !strings.Contains(repo.messages[1].Tool, "[secret saved]") ||
		!strings.Contains(repo.messages[2].Thought, "[secret saved]") {
		t.Errorf("expected [secret saved] placeholders: %+v", repo.messages)
	}
	if !strings.Contains(repo.messages[0].Text, scrubPlain) || !strings.Contains(repo.messages[1].Tool, scrubPlain) {
		t.Errorf("non-secret value must be untouched: %+v", repo.messages)
	}
	if !strings.Contains(repo.messages[0].Text, scrubShort) || !strings.Contains(repo.messages[1].Tool, scrubShort) {
		t.Errorf("secret shorter than 8 chars must be untouched: %+v", repo.messages)
	}
}

func TestWorkflowRunNodeScrubsSecretParams(t *testing.T) {
	repo, cs := scrubFixture(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "workflow_run_node", Arguments: map[string]any{
		"type": scrubNodeTyp, "input": map[string]any{"api_key": scrubKey, "short_key": scrubShort, "name": scrubPlain},
	}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	assertScrubbed(t, repo)
}

// workflow_run_graph takes node params as a JSON object through MCP and scrubs secrets after the run.
func TestWorkflowRunGraphScrubsSecretParams(t *testing.T) {
	repo, cs := scrubFixture(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "workflow_run_graph", Arguments: map[string]any{
		"nodes": []any{map[string]any{"id": "n1", "type": scrubNodeTyp,
			"params": map[string]any{"api_key": scrubKey, "short_key": scrubShort, "name": scrubPlain}}},
	}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	assertScrubbed(t, repo)
}
