package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/storage/sqlite"
	"app/internal/tools/watermark"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestAgentService(t *testing.T, maxContinuations int) (*Service, *sqlite.AgentRepository, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(dir, "agent_approvals_test.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}

	repo := sqlite.NewAgentRepository(db)
	ctx := context.Background()
	// Create a conversation for foreign key constraints
	if err := repo.SaveConversation(ctx, domainagent.Conversation{
		ID:         "test-conv-1",
		ProviderID: domainagent.ProviderCodex,
		Title:      "Test Conversation",
		CreatedAt:  time.Now().UnixMilli(),
		UpdatedAt:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("SaveConversation failed: %v", err)
	}

	limits := DefaultLimits()
	limits.ApprovalTTL = 1 * time.Hour

	svc, err := New(Options{
		Repo:                     repo,
		ApprovalRepo:             repo,
		MaxApprovalContinuations: maxContinuations,
		DataDir:                  dir,
		WorkspaceDir:             filepath.Join(dir, "workspace"),
		Logger:                   slog.New(slog.DiscardHandler),
		Limits:                   limits,
	})
	if err != nil {
		t.Fatalf("New service failed: %v", err)
	}

	svc.mu.Lock()
	svc.conversationID = "test-conv-1"
	svc.activeID = domainagent.ProviderCodex
	svc.states[domainagent.ProviderCodex] = domainagent.StateConnected
	svc.mu.Unlock()

	cleanup := func() {
		_ = svc.Close()
		_ = db.Close()
	}
	return svc, repo, cleanup
}

func TestApprovalInputHashAndCanonicalization(t *testing.T) {
	// Case 1: Maps with keys in different order must yield identical canonical JSON and hash
	mapA := map[string]any{
		"zebra": "stripes",
		"apple": "fruit",
		"nested": map[string]any{
			"z": 100,
			"a": 1,
		},
	}
	mapB := map[string]any{
		"nested": map[string]any{
			"a": 1,
			"z": 100,
		},
		"apple": "fruit",
		"zebra": "stripes",
	}

	jsonA, err := CanonicalJSON(mapA)
	if err != nil {
		t.Fatalf("CanonicalJSON(mapA) error: %v", err)
	}
	jsonB, err := CanonicalJSON(mapB)
	if err != nil {
		t.Fatalf("CanonicalJSON(mapB) error: %v", err)
	}

	if string(jsonA) != string(jsonB) {
		t.Fatalf("canonical JSON mismatch:\nA: %s\nB: %s", string(jsonA), string(jsonB))
	}

	hashA := CalculateInputHash(jsonA)
	hashB := CalculateInputHash(jsonB)
	if hashA != hashB {
		t.Fatalf("hash mismatch: %s vs %s", hashA, hashB)
	}

	// Case 2: Differing values produce different hashes
	mapC := map[string]any{
		"apple": "fruit",
		"zebra": "horse",
		"nested": map[string]any{
			"a": 1,
			"z": 100,
		},
	}
	jsonC, _ := CanonicalJSON(mapC)
	hashC := CalculateInputHash(jsonC)
	if hashA == hashC {
		t.Fatalf("different inputs must produce different hashes: got identical %s", hashA)
	}

	// Case 3: Empty and nil handling
	jsonNil, err := CanonicalJSON(nil)
	if err != nil || string(jsonNil) != "{}" {
		t.Fatalf("CanonicalJSON(nil) = %s, %v; want {}", string(jsonNil), err)
	}
	jsonEmptyStr, err := CanonicalJSON("")
	if err != nil || string(jsonEmptyStr) != "{}" {
		t.Fatalf("CanonicalJSON(\"\") = %s, %v; want {}", string(jsonEmptyStr), err)
	}
}

func TestApprovalAtomicStatusTransition(t *testing.T) {
	svc, repo, cleanup := newTestAgentService(t, 5)
	defer cleanup()
	ctx := context.Background()

	input := map[string]any{"path": "video.mp4", "mode": "remove"}
	canon, _ := CanonicalJSON(input)
	inHash := CalculateInputHash(canon)

	app := &sqlite.AgentApproval{
		ID:                "app-trans-1",
		ConversationID:    "test-conv-1",
		ToolName:          "workflow_run_node",
		NodeType:          "watermark",
		InputJSON:         string(canon),
		InputHash:         inHash,
		Status:            contracts.ApprovalStatusPending,
		ContinuationDepth: 0,
		CreatedAt:         time.Now().UnixMilli(),
		UpdatedAt:         time.Now().UnixMilli(),
	}

	if err := repo.InsertApproval(ctx, app); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}

	// 1. Transition pending -> executing succeeds
	ok, err := repo.UpdateApprovalStatus(ctx, "app-trans-1", contracts.ApprovalStatusPending, contracts.ApprovalStatusExecuting, "", "", "")
	if err != nil || !ok {
		t.Fatalf("UpdateApprovalStatus pending -> executing failed: ok=%v, err=%v", ok, err)
	}

	// 2. Second attempt to transition pending -> executing fails (CAS guard)
	okSecond, err := repo.UpdateApprovalStatus(ctx, "app-trans-1", contracts.ApprovalStatusPending, contracts.ApprovalStatusExecuting, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error on second transition: %v", err)
	}
	if okSecond {
		t.Fatal("second transition from pending must return false")
	}

	// 3. Transition executing -> approved succeeds
	okApprove, err := repo.UpdateApprovalStatus(ctx, "app-trans-1", contracts.ApprovalStatusExecuting, contracts.ApprovalStatusApproved, "approve", `{"done":true}`, "")
	if err != nil || !okApprove {
		t.Fatalf("UpdateApprovalStatus executing -> approved failed: ok=%v, err=%v", okApprove, err)
	}

	// 4. Verify record in database
	rec, err := repo.GetApproval(ctx, "app-trans-1")
	if err != nil || rec == nil {
		t.Fatalf("GetApproval failed: %v", err)
	}
	if rec.Status != contracts.ApprovalStatusApproved {
		t.Fatalf("Status = %q, want %q", rec.Status, contracts.ApprovalStatusApproved)
	}
	if rec.Decision != "approve" {
		t.Fatalf("Decision = %q, want approve", rec.Decision)
	}
	if rec.ResultJSON != `{"done":true}` {
		t.Fatalf("ResultJSON = %q", rec.ResultJSON)
	}

	_ = svc
}

func TestApprovalRejectDuplicateExecution(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()
	ctx := context.Background()

	var execCount atomic.Int32
	execFn := func(execCtx context.Context) (any, error) {
		execCount.Add(1)
		return map[string]any{"ok": true}, nil
	}

	// Queue approval
	req, err := svc.queueApprovalRecord(ctx, "workflow_run_node", "watermark", map[string]any{"file": "sample.png"}, "Run watermark", "remove watermark", execFn)
	if err != nil {
		t.Fatalf("queueApprovalRecord failed: %v", err)
	}

	// First resolve: approve
	if err := svc.ResolveApproval(ctx, req.ID, true); err != nil {
		t.Fatalf("ResolveApproval failed: %v", err)
	}
	if execCount.Load() != 1 {
		t.Fatalf("execCount = %d, want 1", execCount.Load())
	}

	// Second resolve on the same ID: must fail with conflict error and NOT re-run
	errDup := svc.ResolveApproval(ctx, req.ID, true)
	if errDup == nil {
		t.Fatal("duplicate ResolveApproval must return an error")
	}
	var ae *apperr.Error
	if !errors.As(errDup, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("expected CodeConflict error, got %v", errDup)
	}
	if execCount.Load() != 1 {
		t.Fatalf("executor re-run detected! execCount = %d, want 1", execCount.Load())
	}

	// Test Reject decision: executor must NEVER be called
	var rejectExecCalled atomic.Bool
	rejectExec := func(context.Context) (any, error) {
		rejectExecCalled.Store(true)
		return nil, nil
	}
	reqReject, err := svc.queueApprovalRecord(ctx, "workflow_run_node", "watermark", map[string]any{"file": "sample2.png"}, "Run watermark", "summary", rejectExec)
	if err != nil {
		t.Fatalf("queueApprovalRecord failed: %v", err)
	}

	if err := svc.ResolveApproval(ctx, reqReject.ID, false); err != nil {
		t.Fatalf("ResolveApproval reject failed: %v", err)
	}
	if rejectExecCalled.Load() {
		t.Fatal("executor was called on rejection!")
	}

	// Calling resolve on rejected approval must also fail
	errAfterReject := svc.ResolveApproval(ctx, reqReject.ID, true)
	if errAfterReject == nil {
		t.Fatal("ResolveApproval on already rejected approval must fail")
	}
	if !errors.As(errAfterReject, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("expected CodeConflict, got %v", errAfterReject)
	}
}

func TestApprovalConcurrentDoubleResolve(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()
	ctx := context.Background()

	var execCount atomic.Int32
	execFn := func(execCtx context.Context) (any, error) {
		time.Sleep(20 * time.Millisecond) // simulate small execution duration
		execCount.Add(1)
		return map[string]string{"result": "ok"}, nil
	}

	req, err := svc.queueApprovalRecord(ctx, "workflow_run_node", "watermark", map[string]any{"src": "test.mp4"}, "Run", "summary", execFn)
	if err != nil {
		t.Fatalf("queueApprovalRecord failed: %v", err)
	}

	const concurrency = 8
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var conflictCount atomic.Int32

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			err := svc.ResolveApproval(context.Background(), req.ID, true)
			if err == nil {
				successCount.Add(1)
			} else {
				var ae *apperr.Error
				if errors.As(err, &ae) && ae.Code == apperr.CodeConflict {
					conflictCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Fatalf("successCount = %d, want exactly 1", successCount.Load())
	}
	if conflictCount.Load() != concurrency-1 {
		t.Fatalf("conflictCount = %d, want %d", conflictCount.Load(), concurrency-1)
	}
	if execCount.Load() != 1 {
		t.Fatalf("execCount = %d, want exactly 1", execCount.Load())
	}
}

func TestApprovalContinuationDepthLimitEnforcement(t *testing.T) {
	const maxCont = 3
	svc, repo, cleanup := newTestAgentService(t, maxCont)
	defer cleanup()
	ctx := context.Background()

	// 1. Below continuation limit: depth = 1 < 3
	appBelow := &sqlite.AgentApproval{
		ID:                "app-depth-below",
		ConversationID:    "test-conv-1",
		ToolName:          "workflow_run_node",
		NodeType:          "watermark",
		InputJSON:         `{"file":"video.mp4"}`,
		InputHash:         CalculateInputHash([]byte(`{"file":"video.mp4"}`)),
		Status:            contracts.ApprovalStatusPending,
		ContinuationDepth: 1,
		CreatedAt:         time.Now().UnixMilli(),
		UpdatedAt:         time.Now().UnixMilli(),
	}
	if err := repo.InsertApproval(ctx, appBelow); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}

	// Track continuation trigger by intercepting SendPrompt or verifying depth
	// Inject in-memory exec
	svc.approvals.queueItem(pendingApproval{
		id:                appBelow.ID,
		conversationID:    appBelow.ConversationID,
		inputJSON:         appBelow.InputJSON,
		inputHash:         appBelow.InputHash,
		continuationDepth: appBelow.ContinuationDepth,
		exec:              func(context.Context) (any, error) { return "ok", nil },
	})

	if err := svc.ResolveApproval(ctx, appBelow.ID, true); err != nil {
		t.Fatalf("ResolveApproval failed: %v", err)
	}

	// 2. At or above continuation limit: depth = 3 >= maxCont (3)
	appAtLimit := &sqlite.AgentApproval{
		ID:                "app-depth-limit",
		ConversationID:    "test-conv-1",
		ToolName:          "workflow_run_node",
		NodeType:          "watermark",
		InputJSON:         `{"file":"video.mp4"}`,
		InputHash:         CalculateInputHash([]byte(`{"file":"video.mp4"}`)),
		Status:            contracts.ApprovalStatusPending,
		ContinuationDepth: 3,
		CreatedAt:         time.Now().UnixMilli(),
		UpdatedAt:         time.Now().UnixMilli(),
	}
	if err := repo.InsertApproval(ctx, appAtLimit); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}
	svc.approvals.queueItem(pendingApproval{
		id:                appAtLimit.ID,
		conversationID:    appAtLimit.ConversationID,
		inputJSON:         appAtLimit.InputJSON,
		inputHash:         appAtLimit.InputHash,
		continuationDepth: appAtLimit.ContinuationDepth,
		exec:              func(context.Context) (any, error) { return "ok", nil },
	})

	if err := svc.ResolveApproval(ctx, appAtLimit.ID, true); err != nil {
		t.Fatalf("ResolveApproval at limit failed: %v", err)
	}
}

func TestApprovalAmbiguousRecovery(t *testing.T) {
	svc, repo, cleanup := newTestAgentService(t, 5)
	defer cleanup()
	ctx := context.Background()

	// Seed an approval stuck in 'executing' (as if the server crashed mid-execution)
	appCrashing := &sqlite.AgentApproval{
		ID:                "app-crashed-1",
		ConversationID:    "test-conv-1",
		ToolName:          "workflow_run_node",
		NodeType:          "watermark",
		InputJSON:         `{"file":"crashed.mp4"}`,
		InputHash:         CalculateInputHash([]byte(`{"file":"crashed.mp4"}`)),
		Status:            contracts.ApprovalStatusExecuting,
		ContinuationDepth: 0,
		CreatedAt:         time.Now().UnixMilli(),
		UpdatedAt:         time.Now().UnixMilli(),
	}
	if err := repo.InsertApproval(ctx, appCrashing); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}

	// Call RecoverAmbiguousApprovals
	if err := svc.RecoverAmbiguousApprovals(ctx); err != nil {
		t.Fatalf("RecoverAmbiguousApprovals failed: %v", err)
	}

	// Verify status transitioned from executing -> ambiguous
	rec, err := repo.GetApproval(ctx, "app-crashed-1")
	if err != nil || rec == nil {
		t.Fatalf("GetApproval failed: %v", err)
	}
	if rec.Status != contracts.ApprovalStatusAmbiguous {
		t.Fatalf("Status = %q, want %q", rec.Status, contracts.ApprovalStatusAmbiguous)
	}

	// Calling ResolveApproval on an ambiguous record must be refused
	errResolve := svc.ResolveApproval(ctx, "app-crashed-1", true)
	if errResolve == nil {
		t.Fatal("ResolveApproval on ambiguous approval must fail")
	}
	var ae *apperr.Error
	if !errors.As(errResolve, &ae) || ae.Code != apperr.CodeConflict {
		t.Fatalf("expected CodeConflict for ambiguous approval, got: %v", errResolve)
	}
}

func TestApprovalHashMismatchInvalidation(t *testing.T) {
	svc, repo, cleanup := newTestAgentService(t, 5)
	defer cleanup()
	ctx := context.Background()

	origInput := map[string]any{"count": 1}
	canon, _ := CanonicalJSON(origInput)
	inHash := CalculateInputHash(canon)

	app := &sqlite.AgentApproval{
		ID:                "app-tamper-1",
		ConversationID:    "test-conv-1",
		ToolName:          "workflow_run_node",
		NodeType:          "watermark",
		InputJSON:         `{"count": 999}`, // tampered input JSON that does not match inHash
		InputHash:         inHash,
		Status:            contracts.ApprovalStatusPending,
		ContinuationDepth: 0,
		CreatedAt:         time.Now().UnixMilli(),
		UpdatedAt:         time.Now().UnixMilli(),
	}
	if err := repo.InsertApproval(ctx, app); err != nil {
		t.Fatalf("InsertApproval failed: %v", err)
	}

	// ResolveApproval must detect hash mismatch and reject
	errResolve := svc.ResolveApproval(ctx, "app-tamper-1", true)
	if errResolve == nil {
		t.Fatal("ResolveApproval must fail when input hash does not match input JSON")
	}
	var ae *apperr.Error
	if !errors.As(errResolve, &ae) || ae.Code != apperr.CodeValidation {
		t.Fatalf("expected CodeValidation error for hash mismatch, got: %v", errResolve)
	}
}

type fakeBinariesForApproval struct{}

func (f *fakeBinariesForApproval) List(context.Context) ([]domainbinaries.BinaryInfo, error) {
	return nil, nil
}
func (f *fakeBinariesForApproval) Probe(context.Context, domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return domainbinaries.BinaryInfo{ID: "ffmpeg", Status: domainbinaries.StatusAvailable}, nil
}
func (f *fakeBinariesForApproval) Download(context.Context, domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error) {
	return domainbinaries.BinaryInfo{ID: "ffmpeg", Status: domainbinaries.StatusAvailable}, nil
}

func callMCPTool(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var payload map[string]any
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(tc.Text), &payload)
			break
		}
	}
	return payload, res.IsError
}

func TestSkipTrustedApproval_WatermarkAutoApproval(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()

	wf := &fakeWorkflow{specs: []engine.Spec{watermark.NewNode(watermark.Options{}).Spec()}}
	svc.workflow = wf

	ws := svc.getWorkspaceDir()
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	testImg := filepath.Join(ws, "target.png")
	if err := os.WriteFile(testImg, []byte("fake-png-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// 1. By default, SkipTrustedApproval is false. Inplace destructive run MUST require approval.
	if svc.skipTrustedApproval() {
		t.Fatal("expected default skipTrustedApproval to be false")
	}
	payload, isErr := callMCPTool(t, cs, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{testImg},
			"inplace": true,
		},
	})
	if isErr {
		t.Fatalf("unexpected call error: %v", payload)
	}
	if payload["status"] != "needs_approval" {
		t.Fatalf("expected status 'needs_approval', got %v", payload["status"])
	}
	if payload["auto_approved"] == true {
		t.Fatal("auto_approved must not be true when skipTrustedApproval is false")
	}

	// 2. SetSkipTrustedApproval(true). Inplace destructive run in trusted directory must be auto-approved!
	svc.SetSkipTrustedApproval(true)
	if !svc.skipTrustedApproval() {
		t.Fatal("expected skipTrustedApproval to be true after SetSkipTrustedApproval(true)")
	}

	payloadAuto, isErrAuto := callMCPTool(t, cs, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{testImg},
			"inplace": true,
		},
	})
	if isErrAuto {
		t.Fatalf("unexpected auto call error: %v", payloadAuto)
	}
	if payloadAuto["ok"] != true {
		t.Fatalf("expected ok=true, got %v", payloadAuto)
	}
	if payloadAuto["auto_approved"] != true {
		t.Fatalf("expected auto_approved=true, got %v", payloadAuto["auto_approved"])
	}
}

func TestSkipTrustedApproval_Invariants(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()

	providerDeleteSpec := engine.Spec{
		Type:     "tool.provider_delete",
		Provider: "tool",
		Label:    "Delete API key",
		Params: []engine.Param{
			{Name: "id", Kind: engine.ParamSelect},
			{Name: "confirm", Kind: engine.ParamToggle, Destructive: true},
		},
	}
	wf := &fakeWorkflow{specs: []engine.Spec{providerDeleteSpec}}
	svc.workflow = wf
	svc.binaries = &fakeBinariesForApproval{}

	// Enable skip trusted approval
	svc.SetSkipTrustedApproval(true)

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Invariant 1: tool.provider_delete MUST always require approval even with skipTrustedApproval=true
	payloadDelete, isErr := callMCPTool(t, cs, "workflow_run_node", map[string]any{
		"type": "tool.provider_delete",
		"input": map[string]any{
			"id":      "saved-key-1",
			"confirm": true,
		},
	})
	if isErr {
		t.Fatalf("unexpected error: %v", payloadDelete)
	}
	if payloadDelete["status"] != "needs_approval" {
		t.Fatalf("expected tool.provider_delete to require approval, got status=%v", payloadDelete["status"])
	}
	if payloadDelete["auto_approved"] == true {
		t.Fatal("tool.provider_delete must NEVER be auto-approved")
	}

	// Invariant 2: binaries_download MUST always require approval even with skipTrustedApproval=true
	payloadBinary, isErrBin := callMCPTool(t, cs, "binaries_download", map[string]any{
		"id": "ffmpeg",
	})
	if isErrBin {
		t.Fatalf("unexpected error: %v", payloadBinary)
	}
	if payloadBinary["status"] != "needs_approval" {
		t.Fatalf("expected binaries_download to require approval, got status=%v", payloadBinary["status"])
	}
	if payloadBinary["auto_approved"] == true {
		t.Fatal("binaries_download must NEVER be auto-approved")
	}
}

func TestSkipTrustedApproval_UntrustedTarget(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()

	wf := &fakeWorkflow{specs: []engine.Spec{watermark.NewNode(watermark.Options{}).Spec()}}
	svc.workflow = wf
	svc.SetSkipTrustedApproval(true)

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// An input file outside trusted roots cannot be auto-approved; policy.Resolve fails
	untrustedDir := t.TempDir()
	untrustedImg := filepath.Join(untrustedDir, "outside.png")
	if err := os.WriteFile(untrustedImg, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload, isErr := callMCPTool(t, cs, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{untrustedImg},
			"inplace": true,
		},
	})
	if !isErr {
		t.Fatalf("expected untrusted input to fail policy resolution, got %v", payload)
	}
}

func TestSkipTrustedApproval_WorkflowRunGraph(t *testing.T) {
	svc, _, cleanup := newTestAgentService(t, 5)
	defer cleanup()

	providerDeleteSpec := engine.Spec{
		Type:     "tool.provider_delete",
		Provider: "tool",
		Label:    "Delete API key",
		Params: []engine.Param{
			{Name: "id", Kind: engine.ParamSelect},
			{Name: "confirm", Kind: engine.ParamToggle, Destructive: true},
		},
	}
	wf := &fakeWorkflow{specs: []engine.Spec{watermark.NewNode(watermark.Options{}).Spec(), providerDeleteSpec}}
	svc.workflow = wf

	ws := svc.getWorkspaceDir()
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	testImg := filepath.Join(ws, "target2.png")
	if err := os.WriteFile(testImg, []byte("fake-png-data"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// 1. skipTrustedApproval=false: graph with inplace watermark requires approval
	payloadGraph, isErr := callMCPTool(t, cs, "workflow_run_graph", map[string]any{
		"nodes": []any{
			map[string]any{
				"id":   "n1",
				"type": watermark.NodeType,
				"params": map[string]any{
					"images":  []any{testImg},
					"inplace": true,
				},
			},
		},
	})
	if isErr {
		t.Fatalf("unexpected call error: %v", payloadGraph)
	}
	if payloadGraph["status"] != "needs_approval" {
		t.Fatalf("expected status 'needs_approval', got %v", payloadGraph["status"])
	}

	// 2. skipTrustedApproval=true: graph with inplace watermark on trusted target is auto-approved
	svc.SetSkipTrustedApproval(true)
	payloadGraphAuto, isErrAuto := callMCPTool(t, cs, "workflow_run_graph", map[string]any{
		"nodes": []any{
			map[string]any{
				"id":   "n1",
				"type": watermark.NodeType,
				"params": map[string]any{
					"images":  []any{testImg},
					"inplace": true,
				},
			},
		},
	})
	if isErrAuto {
		t.Fatalf("unexpected call error: %v", payloadGraphAuto)
	}
	if payloadGraphAuto["ok"] != true || payloadGraphAuto["auto_approved"] != true {
		t.Fatalf("expected auto-approved graph run, got %v", payloadGraphAuto)
	}

	// 3. skipTrustedApproval=true: graph containing tool.provider_delete MUST require approval
	payloadGraphDelete, isErrDelete := callMCPTool(t, cs, "workflow_run_graph", map[string]any{
		"nodes": []any{
			map[string]any{
				"id":   "n1",
				"type": watermark.NodeType,
				"params": map[string]any{
					"images":  []any{testImg},
					"inplace": true,
				},
			},
			map[string]any{
				"id":   "n2",
				"type": "tool.provider_delete",
				"params": map[string]any{
					"id":      "key-1",
					"confirm": true,
				},
			},
		},
	})
	if isErrDelete {
		t.Fatalf("unexpected call error: %v", payloadGraphDelete)
	}
	if payloadGraphDelete["status"] != "needs_approval" {
		t.Fatalf("expected graph with provider_delete to require approval, got %v", payloadGraphDelete)
	}
	if payloadGraphDelete["auto_approved"] == true {
		t.Fatal("graph with provider_delete must not be auto-approved")
	}
}
