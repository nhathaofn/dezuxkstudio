package agent

import (
	"log/slog"
	"testing"
)

func TestDezuxkToolPermissionMatchesExactTools(t *testing.T) {
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	own := map[string]bool{}
	for _, name := range svc.toolNames() {
		own[name] = true
	}
	allowed := []string{
		"MCP Tool workflow_run_node",
		"MCP Tool studio_list_capabilities",
		"dezuxk/studio_list_capabilities",
		"mcp__dezuxk__workspace_list_files",
		"File ../../../../DezuxkStudio/data/agent-profiles/antigravity/home/.gemini/antigravity-cli/mcp/dezuxk/workflow_list_specs.json",
	}
	for _, title := range allowed {
		if !isDezuxkToolPermission(title, own) {
			t.Errorf("expected %q to be approved", title)
		}
	}
	denied := []string{
		"run_command: git status",
		"Terminal cd D:/nhathao/project/dezuxkstudio && go build",
		"File D:/nhathao/project/dezuxkstudio/main.go",
		"Edit D:/nhathao/project/dezuxkstudio/internal/service/agent/profile/agent.md",
		"File C:/Users/PC/AppData/Local/DezuxkStudio/data/agent-workspace/notes.txt",
		"MCP Tool workflow_run_everything",
		"MCP Tool other_server__workflow_run_node",
		"write_to_file dezuxk/workflow_run_node.json",
		"File ../antigravity-cli/mcp/dezuxk/evil.json",
		"",
	}
	for _, title := range denied {
		if isDezuxkToolPermission(title, own) {
			t.Errorf("expected %q to be refused", title)
		}
	}
}
