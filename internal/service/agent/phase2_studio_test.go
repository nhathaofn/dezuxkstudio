package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"app/internal/contracts"
)

type stubPhase2AgentStudio struct {
	stubUntrustedStudioManager
	autoApprove  bool
	enqueueCalls int
	lastApproved bool
}

func (s *stubPhase2AgentStudio) GetEpisode(_ context.Context, id string) (contracts.StudioEpisodeDetailResult, error) {
	return contracts.StudioEpisodeDetailResult{
		Project: contracts.StudioProject{
			ID:                 "proj-p2",
			Title:              "Phase2",
			RootDir:            s.untrustedRoot,
			AutoApproveBatches: s.autoApprove,
		},
		Episode: contracts.StudioEpisode{
			ID:        id,
			ProjectID: "proj-p2",
			Code:      "ep01",
		},
	}, nil
}

func (s *stubPhase2AgentStudio) EnqueueBatch(_ context.Context, p contracts.StudioQueueEnqueuePayload) (contracts.StudioQueueEnqueueResult, error) {
	s.enqueueCalls++
	s.lastApproved = p.Approved
	return contracts.StudioQueueEnqueueResult{Summary: "enqueued 1"}, nil
}
func (s *stubPhase2AgentStudio) ListJobs(context.Context, contracts.StudioQueueListPayload) (contracts.StudioQueueListResult, error) {
	return contracts.StudioQueueListResult{}, nil
}
func (s *stubPhase2AgentStudio) ControlQueue(context.Context, contracts.StudioQueueControlPayload) (contracts.StudioQueueControlResult, error) {
	return contracts.StudioQueueControlResult{Affected: 1}, nil
}
func (s *stubPhase2AgentStudio) GetCostSummary(context.Context, contracts.StudioCostSummaryPayload) (contracts.StudioCostSummaryResult, error) {
	return contracts.StudioCostSummaryResult{Currency: "USD"}, nil
}
func (s *stubPhase2AgentStudio) ListVersions(context.Context, contracts.StudioVersionsListPayload) (contracts.StudioVersionsListResult, error) {
	return contracts.StudioVersionsListResult{}, nil
}
func (s *stubPhase2AgentStudio) SelectVersion(context.Context, contracts.StudioVersionSelectPayload) (contracts.StudioVersionSelectResult, error) {
	return contracts.StudioVersionSelectResult{}, nil
}
func (s *stubPhase2AgentStudio) RunQCReview(context.Context, contracts.StudioQCReviewPayload) (contracts.StudioQCReport, error) {
	return contracts.StudioQCReport{Verdict: "pass", OverallScore: 9.0}, nil
}
func (s *stubPhase2AgentStudio) RunBatchPreflight(context.Context, contracts.StudioBatchPreflightPayload) (contracts.BatchPreflightOutput, error) {
	return contracts.BatchPreflightOutput{Ready: true, EstimatedUSD: 0.2}, nil
}

func TestStudioPhase2MCPToolsAndVisualQCCustomizations(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	mgr := &stubPhase2AgentStudio{
		stubUntrustedStudioManager: stubUntrustedStudioManager{untrustedRoot: filepath.Join(ws, "projects", "p1")},
		autoApprove:                true,
	}
	svc, err := New(Options{
		Repo:         newFakeRepo(),
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: ws,
		Studio:       mgr,
		Logger:       slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()

	snap, err := svc.ListCustomizations(context.Background())
	if err != nil {
		t.Fatalf("ListCustomizations: %v", err)
	}
	var foundSkill, foundSub bool
	for _, sk := range snap.Skills {
		if sk.Name == "visual-qc" {
			foundSkill = true
		}
	}
	for _, sb := range snap.Subagents {
		if sb.Name == "visual-reviewer" {
			foundSub = true
		}
	}
	if !foundSkill || !foundSub {
		t.Fatalf("expected built-in visual-qc skill and visual-reviewer subagent, got skill=%v sub=%v", foundSkill, foundSub)
	}

	// Verify MCP exposes studio Phase 2 tools and approval gate passes Approved=true
	ctx := context.Background()
	sT, cT := mcp.NewInMemoryTransports()
	ss, err := svc.buildMCPServer().Connect(ctx, sT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, cT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tl := range tools.Tools {
		seen[tl.Name] = true
	}
	for _, want := range []string{
		"studio_batch_preflight", "studio_queue_enqueue", "studio_queue_list",
		"studio_queue_control", "studio_cost_summary", "studio_versions_list",
		"studio_version_select", "studio_qc_review",
	} {
		if !seen[want] {
			t.Fatalf("missing Phase 2 MCP tool %q", want)
		}
	}

	// studio_queue_enqueue executes with AutoApproveBatches=true inside trusted folder and sets Approved=true
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_queue_enqueue",
		Arguments: map[string]any{"episode_id": "ep1", "stage": "generate_videos"},
	}); err != nil {
		t.Fatalf("CallTool studio_queue_enqueue: %v", err)
	}
	if mgr.enqueueCalls != 1 || !mgr.lastApproved {
		t.Fatalf("expected EnqueueBatch called with Approved=true, got calls=%d approved=%v", mgr.enqueueCalls, mgr.lastApproved)
	}

	// Approval gate test: when AutoApproveBatches=false, studio_queue_enqueue pauses for approval,
	// and ResolveApproval(..., true) executes EnqueueBatch with Approved=true.
	mgr.autoApprove = false
	mgr.enqueueCalls = 0
	mgr.lastApproved = false
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "studio_queue_enqueue",
		Arguments: map[string]any{"episode_id": "ep1", "stage": "generate_videos"},
	}); err != nil {
		t.Fatalf("CallTool gated studio_queue_enqueue: %v", err)
	}
	if mgr.enqueueCalls != 0 {
		t.Fatalf("expected 0 direct enqueue calls before approval, got %d", mgr.enqueueCalls)
	}
	pending := svc.ListApprovals()
	if len(pending) != 1 || pending[0].Tool != "studio_queue_enqueue" {
		t.Fatalf("expected 1 pending studio_queue_enqueue approval, got %+v", pending)
	}
	if err := svc.ResolveApproval(ctx, pending[0].ID, true); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if mgr.enqueueCalls != 1 || !mgr.lastApproved {
		t.Fatalf("expected approved EnqueueBatch call with Approved=true, got calls=%d approved=%v", mgr.enqueueCalls, mgr.lastApproved)
	}
}
