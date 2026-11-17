package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	"app/internal/engine"
	"app/internal/storage/sqlite"

	"github.com/google/uuid"
)

// ApprovalRepository defines persistence operations for agent MCP approvals.
type ApprovalRepository interface {
	InsertApproval(ctx context.Context, app *sqlite.AgentApproval) error
	GetApproval(ctx context.Context, id string) (*sqlite.AgentApproval, error)
	ListPendingApprovals(ctx context.Context, conversationID string) ([]*sqlite.AgentApproval, error)
	UpdateApprovalStatus(ctx context.Context, id string, oldStatus, newStatus string, decision string, resultJSON string, errorText string) (bool, error)
	RecoverAmbiguousApprovals(ctx context.Context) error
}

// CanonicalJSON recursively formats any JSON-encodable value into deterministic canonical JSON (sorted keys).
func CanonicalJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("{}"), nil
	}
	switch val := v.(type) {
	case []byte:
		if len(val) == 0 {
			return []byte("{}"), nil
		}
		var obj any
		if err := json.Unmarshal(val, &obj); err != nil {
			return nil, err
		}
		return json.Marshal(obj)
	case string:
		if val == "" {
			return []byte("{}"), nil
		}
		var obj any
		if err := json.Unmarshal([]byte(val), &obj); err != nil {
			return json.Marshal(val)
		}
		return json.Marshal(obj)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var obj any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
		return json.Marshal(obj)
	}
}

// CalculateInputHash produces a hex-encoded SHA-256 hash of the canonical JSON data.
func CalculateInputHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type pendingApproval struct {
	id                string
	conversationID    string
	providerID        domainagent.ProviderID
	tool              string
	nodeType          string
	title             string
	summary           string
	inputJSON         string
	inputHash         string
	continuationDepth int
	createdAt         time.Time
	expiresAt         time.Time
	exec              func(context.Context) (any, error)
}

// approvalStore holds high-impact actions proposed by an agent until the user approves or rejects them.
// Requests expire after ttl and are dropped when their provider disconnects or signs out, so an action
// proposed under one account/session can never be executed later under another.
type approvalStore struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int // pending requests kept at most, so a looping agent cannot flood the panel
	now   func() time.Time
	items map[string]pendingApproval
}

func newApprovalStore(ttl time.Duration, max int) *approvalStore {
	return &approvalStore{ttl: ttl, max: max, now: time.Now, items: make(map[string]pendingApproval)}
}

// queue adds a request; ok is false (nothing queued) when max requests are already pending.
func (a *approvalStore) queue(providerID domainagent.ProviderID, tool, title, summary string, exec func(context.Context) (any, error)) (domainagent.ApprovalRequest, bool) {
	now := a.now()
	req := domainagent.ApprovalRequest{
		ID:         uuid.NewString(),
		ProviderID: providerID,
		Tool:       tool,
		Title:      title,
		Summary:    summary,
		CreatedAt:  now.UTC().Format(time.RFC3339),
	}
	item := pendingApproval{
		id:         req.ID,
		providerID: providerID,
		tool:       tool,
		title:      title,
		summary:    summary,
		createdAt:  now,
		exec:       exec,
	}
	if !a.queueItem(item) {
		return req, false
	}
	return req, true
}

// queueItem adds a pending approval item; ok is false (nothing queued) when max requests are already pending.
func (a *approvalStore) queueItem(item pendingApproval) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.pruneLocked(now)
	if a.max > 0 && len(a.items) >= a.max {
		return false
	}
	if a.ttl > 0 {
		item.expiresAt = now.Add(a.ttl)
	}
	a.items[item.id] = item
	return true
}

func (a *approvalStore) list() []domainagent.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.now())
	out := make([]domainagent.ApprovalRequest, 0, len(a.items))
	for _, it := range a.items {
		out = append(out, domainagent.ApprovalRequest{
			ID:         it.id,
			ProviderID: it.providerID,
			Tool:       it.tool,
			Title:      it.title,
			Summary:    it.summary,
			CreatedAt:  it.createdAt.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (a *approvalStore) get(id string) (pendingApproval, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.now())
	it, ok := a.items[id]
	return it, ok
}

func (a *approvalStore) pruneLocked(now time.Time) {
	for id, it := range a.items {
		if a.ttl > 0 && !it.expiresAt.IsZero() && !now.Before(it.expiresAt) {
			delete(a.items, id)
		}
	}
}

// consume removes and returns the pending approval atomically before execution so a replay never runs twice.
// Expired requests are not returned.
func (a *approvalStore) consume(id string) (pendingApproval, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.now())
	it, ok := a.items[id]
	if ok {
		delete(a.items, id)
	}
	return it, ok
}

func (a *approvalStore) clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.items)
}

// clearProvider drops every pending request proposed by providerID and reports whether any was removed.
func (a *approvalStore) clearProvider(providerID domainagent.ProviderID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := false
	for id, it := range a.items {
		if it.providerID == providerID {
			delete(a.items, id)
			removed = true
		}
	}
	return removed
}

// ListApprovals returns pending high-impact actions waiting for user approval in the desktop UI.
func (s *Service) ListApprovals() []domainagent.ApprovalRequest {
	if s.approvalRepo != nil {
		s.mu.Lock()
		convID := s.conversationID
		s.mu.Unlock()
		list, err := s.approvalRepo.ListPendingApprovals(context.Background(), convID)
		if err == nil && len(list) > 0 {
			out := make([]domainagent.ApprovalRequest, 0, len(list))
			now := time.Now().UnixMilli()
			for _, rec := range list {
				// Filter expired if TTL configured
				if s.limits.ApprovalTTL > 0 && now-rec.CreatedAt > s.limits.ApprovalTTL.Milliseconds() {
					continue
				}
				s.mu.Lock()
				pid := s.activeID
				s.mu.Unlock()
				created := time.UnixMilli(rec.CreatedAt).UTC().Format(time.RFC3339)
				title := "Run " + rec.ToolName
				summary := rec.NodeType
				if mem, ok := s.approvals.get(rec.ID); ok {
					if mem.title != "" {
						title = mem.title
					}
					if mem.summary != "" {
						summary = mem.summary
					}
					if mem.providerID != "" {
						pid = mem.providerID
					}
				}
				out = append(out, domainagent.ApprovalRequest{
					ID:         rec.ID,
					ProviderID: pid,
					Tool:       rec.ToolName,
					Title:      title,
					Summary:    summary,
					CreatedAt:  created,
				})
			}
			return out
		}
	}

	// Fallback to in-memory items
	a := s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.now())
	out := make([]domainagent.ApprovalRequest, 0, len(a.items))
	for _, it := range a.items {
		out = append(out, domainagent.ApprovalRequest{
			ID:         it.id,
			ProviderID: it.providerID,
			Tool:       it.tool,
			Title:      it.title,
			Summary:    it.summary,
			CreatedAt:  it.createdAt.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ResolveApproval approves or rejects a pending action proposed by an agent MCP tool call.
func (s *Service) ResolveApproval(ctx context.Context, id string, approve bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return userError(apperr.CodeValidation, "approval_id_empty", "approval ID cannot be empty", nil)
	}

	if s.approvalRepo == nil {
		// Fallback for tests without repository
		item, ok := s.approvals.consume(id)
		s.emitApprovals()
		if !ok {
			return userError(apperr.CodeNotFound, msgApprovalGone, "approval request is no longer pending", nil)
		}
		if !approve {
			return nil
		}
		if _, err := item.exec(ctx); err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				return ae
			}
			return apperr.Wrap(apperr.CodeUnavailable, "approved action failed", err)
		}
		return nil
	}

	// 1. Fetch approval record from SQLite
	rec, err := s.approvalRepo.GetApproval(ctx, id)
	if err != nil {
		return fmt.Errorf("agent: query approval %q: %w", id, err)
	}
	if rec == nil {
		return userError(apperr.CodeNotFound, msgApprovalGone, "approval request is no longer pending", nil)
	}

	// 2. Reject concurrent/duplicate executions or invalid status
	switch rec.Status {
	case contracts.ApprovalStatusPending:
		// Expected status, continue
	case contracts.ApprovalStatusExecuting:
		return userError(apperr.CodeConflict, "agent.errors.approval_busy", "approval request is currently executing", nil)
	case contracts.ApprovalStatusAmbiguous:
		return userError(apperr.CodeConflict, "agent.errors.approval_ambiguous", "approval execution status is ambiguous; requires user confirmation", nil)
	case contracts.ApprovalStatusApproved, contracts.ApprovalStatusRejected, contracts.ApprovalStatusFailed:
		return userError(apperr.CodeConflict, "agent.errors.approval_already_resolved", fmt.Sprintf("approval request was already resolved as %s", rec.Status), nil)
	default:
		return userError(apperr.CodeConflict, "approval_not_pending", fmt.Sprintf("approval request is in %s state, not pending", rec.Status), nil)
	}

	// 3. Re-verify input hash matches snapshot
	if rec.InputJSON != "" && rec.InputHash != "" {
		canon, canonErr := CanonicalJSON(rec.InputJSON)
		if canonErr == nil {
			currHash := CalculateInputHash(canon)
			if currHash != rec.InputHash {
				return userError(apperr.CodeValidation, "agent.errors.approval_hash_mismatch", "input snapshot hash mismatch; approval request is invalidated", nil)
			}
		}
	}

	// 4. Check TTL expiration
	if s.limits.ApprovalTTL > 0 {
		now := time.Now().UnixMilli()
		if now-rec.CreatedAt > s.limits.ApprovalTTL.Milliseconds() {
			_, _ = s.approvalRepo.UpdateApprovalStatus(ctx, id, contracts.ApprovalStatusPending, contracts.ApprovalStatusFailed, "expired", "", "approval TTL expired")
			s.emitApprovals()
			return userError(apperr.CodeValidation, "agent.errors.approval_expired", "approval request has expired", nil)
		}
	}

	decisionStr := "approve"
	if !approve {
		decisionStr = "reject"
	}

	// 5. Atomic CAS transition:
	// If approve: pending -> executing
	// If reject: pending -> rejected
	targetStatus := contracts.ApprovalStatusExecuting
	if !approve {
		targetStatus = contracts.ApprovalStatusRejected
	}

	updated, err := s.approvalRepo.UpdateApprovalStatus(ctx, id, contracts.ApprovalStatusPending, targetStatus, decisionStr, "", "")
	if err != nil {
		return fmt.Errorf("agent: update approval status: %w", err)
	}
	if !updated {
		return userError(apperr.CodeConflict, "agent.errors.approval_concurrent", "approval request status changed concurrently", nil)
	}

	// Remove from in-memory store now that it's no longer pending
	memItem, _ := s.approvals.consume(id)
	s.emitApprovals()

	maxContinuations := s.maxApprovalContinuations
	if maxContinuations <= 0 {
		maxContinuations = 5
	}

	if !approve {
		// On Reject: do NOT call executor.
		if rec.ContinuationDepth < maxContinuations {
			feedback := fmt.Sprintf("[System: User REJECTED tool execution for %q (approval id: %s). The operation was cancelled. Do NOT retry this operation or attempt to use equivalent tools to circumvent the user's rejection without explicit instructions from the user.]", rec.ToolName, rec.ID)
			s.triggerContinuationTurn(rec.ConversationID, feedback, rec.ContinuationDepth+1)
		}
		return nil
	}

	// On Approve: status is currently 'executing'. Execute the underlying action.
	var (
		res     any
		execErr error
	)

	if memItem.exec != nil {
		res, execErr = memItem.exec(ctx)
	} else if s.workflow != nil && rec.ToolName == "workflow_run_node" {
		res, execErr = s.workflow.RunNode(ctx, rec.NodeType, json.RawMessage(rec.InputJSON))
	} else if s.workflow != nil && rec.ToolName == "workflow_run_graph" {
		var g engine.Graph
		if err := json.Unmarshal([]byte(rec.InputJSON), &g); err != nil {
			execErr = fmt.Errorf("invalid graph JSON: %w", err)
		} else {
			res, execErr = s.workflow.RunGraph(ctx, g)
		}
	} else if s.binaries != nil && rec.ToolName == "binaries_download" {
		var in binaryIDToolInput
		if err := json.Unmarshal([]byte(rec.InputJSON), &in); err != nil {
			execErr = fmt.Errorf("invalid binary input: %w", err)
		} else {
			res, execErr = s.binaries.Download(ctx, domainbinaries.BinaryID(in.ID))
		}
	} else if s.studio != nil && rec.ToolName == "studio_episode_run_stage" {
		var p contracts.StudioEpisodeRunStagePayload
		if err := json.Unmarshal([]byte(rec.InputJSON), &p); err != nil {
			execErr = fmt.Errorf("invalid studio stage input: %w", err)
		} else {
			res, execErr = s.studio.RunStage(ctx, p)
		}
	} else {
		execErr = fmt.Errorf("no executor available for approved action %s", rec.ToolName)
	}

	if execErr != nil {
		// Update status executing -> failed
		_, _ = s.approvalRepo.UpdateApprovalStatus(ctx, id, contracts.ApprovalStatusExecuting, contracts.ApprovalStatusFailed, decisionStr, "", execErr.Error())
		s.emitApprovals()

		if rec.ContinuationDepth < maxContinuations {
			feedback := fmt.Sprintf("[System: User approved tool execution for %q (approval id: %s), but execution failed: %v]", rec.ToolName, rec.ID, execErr)
			s.triggerContinuationTurn(rec.ConversationID, feedback, rec.ContinuationDepth+1)
		}

		var ae *apperr.Error
		if errors.As(execErr, &ae) {
			return ae
		}
		return apperr.Wrap(apperr.CodeUnavailable, "approved action failed", execErr)
	}

	// Execution succeeded: update executing -> approved
	resBytes, _ := json.Marshal(res)
	resJSON := string(resBytes)
	_, _ = s.approvalRepo.UpdateApprovalStatus(ctx, id, contracts.ApprovalStatusExecuting, contracts.ApprovalStatusApproved, decisionStr, resJSON, "")
	s.emitApprovals()

	if rec.ContinuationDepth < maxContinuations {
		feedback := fmt.Sprintf("[System: User approved tool execution for %q (approval id: %s). Result:\n%s]", rec.ToolName, rec.ID, resJSON)
		s.triggerContinuationTurn(rec.ConversationID, feedback, rec.ContinuationDepth+1)
	}

	return nil
}

// RecoverAmbiguousApprovals transitions any executing approvals left in limbo to 'ambiguous'.
func (s *Service) RecoverAmbiguousApprovals(ctx context.Context) error {
	if s.approvalRepo != nil {
		return s.approvalRepo.RecoverAmbiguousApprovals(ctx)
	}
	return nil
}

func (s *Service) emitApprovals() {
	if s.onApprovalsChanged != nil {
		s.onApprovalsChanged(s.ListApprovals())
	}
}
