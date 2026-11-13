package agent

import (
	"context"
	"sort"
	"sync"
	"time"

	domainagent "app/internal/domain/agent"

	"app/internal/apperr"
	"github.com/google/uuid"
)

type pendingApproval struct {
	request   domainagent.ApprovalRequest
	exec      func(context.Context) (any, error)
	expiresAt time.Time
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
func (a *approvalStore) queue(providerID domainagent.ProviderID, tool, title, summary string, exec func(context.Context) (any, error)) (req domainagent.ApprovalRequest, ok bool) {
	now := a.now()
	req = domainagent.ApprovalRequest{
		ID:         uuid.NewString(),
		ProviderID: providerID,
		Tool:       tool,
		Title:      title,
		Summary:    summary,
		CreatedAt:  now.UTC().Format(time.RFC3339),
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(now)
	if a.max > 0 && len(a.items) >= a.max {
		return req, false
	}
	a.items[req.ID] = pendingApproval{request: req, exec: exec, expiresAt: now.Add(a.ttl)}
	return req, true
}

func (a *approvalStore) pruneLocked(now time.Time) {
	for id, it := range a.items {
		if a.ttl > 0 && !now.Before(it.expiresAt) {
			delete(a.items, id)
		}
	}
}

func (a *approvalStore) list() []domainagent.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.now())
	out := make([]domainagent.ApprovalRequest, 0, len(a.items))
	for _, it := range a.items {
		out = append(out, it.request)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
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
		if it.request.ProviderID == providerID {
			delete(a.items, id)
			removed = true
		}
	}
	return removed
}

// ListApprovals returns pending high-impact actions waiting for user approval in the desktop UI.
func (s *Service) ListApprovals() []domainagent.ApprovalRequest {
	return s.approvals.list()
}

// ResolveApproval approves or rejects a pending action proposed by an agent MCP tool call.
func (s *Service) ResolveApproval(ctx context.Context, id string, approve bool) (any, error) {
	item, ok := s.approvals.consume(id)
	s.emitApprovals()
	if !ok {
		return nil, userError(apperr.CodeNotFound, msgApprovalGone, "approval request is no longer pending", nil)
	}
	if !approve {
		return map[string]any{"approved": false}, nil
	}
	res, err := item.exec(ctx)
	if err != nil {
		return nil, apperr.Unavailable(err.Error())
	}
	return res, nil
}

func (s *Service) emitApprovals() {
	if s.onApprovalsChanged != nil {
		s.onApprovalsChanged(s.approvals.list())
	}
}
