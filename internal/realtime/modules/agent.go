package modules

import (
	"app/internal/apperr"
	"context"

	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	"app/internal/realtime"
	agentsvc "app/internal/service/agent"
)

// Agent exposes agent:* actions over realtime WebSocket.
type Agent struct {
	Service *agentsvc.Service
	// LockTrustedFolders refuses agent:add_allowed_dir / agent:remove_allowed_dir. Set in headless
	// server mode: trusted folders also bound what remote clients may touch (the workflow file guard),
	// so a remote client must not be able to widen its own boundary.
	LockTrustedFolders bool
}

func (m Agent) trustedFoldersLocked() error {
	if m.LockTrustedFolders {
		return apperr.Unavailable("trusted folders can only be changed in the desktop app on this machine")
	}
	return nil
}

func (Agent) Name() string { return "agent" }

func (m Agent) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionAgentList,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentListResult, error) {
			return m.listResult(ctx)
		})

	realtime.Handle(r, contracts.ActionAgentProbe,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Probe(ctx, p.ID)
			if err != nil {
				return contracts.AgentProviderInfo{}, err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentSetPath,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSetPathPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.SetCustomPath(ctx, p.ID, p.CustomPath)
			if err != nil {
				return contracts.AgentProviderInfo{}, err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentLogin,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentLoginPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Login(ctx, p.ID, p.APIKey)
			if err != nil {
				return contracts.NewAgentProviderInfo(info), err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentLoginCode,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentLoginCodePayload) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.SubmitLoginCode(ctx, p.ID, p.Code)
		})

	realtime.Handle(r, contracts.ActionAgentLoginCancel,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.Empty, error) {
			m.Service.CancelLogin(ctx, p.ID)
			return contracts.Empty{}, nil
		})

	realtime.Handle(r, contracts.ActionAgentLogout,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Logout(ctx, p.ID)
			if err != nil {
				return contracts.NewAgentProviderInfo(info), err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentSetModel,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSetModelPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.SetModel(ctx, p.ID, p.Model, p.Effort)
			if err != nil {
				return contracts.AgentProviderInfo{}, err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentConnect,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Connect(ctx, p.ID)
			if err != nil {
				return contracts.NewAgentProviderInfo(info), err
			}
			return contracts.NewAgentProviderInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentDisconnect,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentListResult, error) {
			if err := m.Service.DisconnectByUser(ctx); err != nil {
				return contracts.AgentListResult{}, err
			}
			return m.listResult(ctx)
		})

	realtime.Handle(r, contracts.ActionAgentSendPrompt,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSendPromptPayload) (contracts.AgentPromptResult, error) {
			msgID, provID, err := m.Service.SendPrompt(ctx, p.Prompt)
			if err != nil {
				return contracts.AgentPromptResult{}, err
			}
			return contracts.AgentPromptResult{
				MessageID:      msgID,
				ProviderID:     provID,
				ConversationID: m.Service.CurrentConversationID(),
			}, nil
		})

	realtime.Handle(r, contracts.ActionAgentCancelPrompt,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.CancelPrompt(ctx)
		})

	realtime.Handle(r, contracts.ActionAgentNewConversation,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.NewConversation(ctx)
		})

	realtime.Handle(r, contracts.ActionAgentListConversations,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentConversationListResult, error) {
			list, err := m.Service.ListConversations(ctx)
			if err != nil {
				return contracts.AgentConversationListResult{}, err
			}
			out := make([]contracts.AgentConversation, len(list))
			for i, c := range list {
				out[i] = contracts.AgentConversation(c)
			}
			return contracts.AgentConversationListResult{Conversations: out}, nil
		})

	realtime.Handle(r, contracts.ActionAgentOpenConversation,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentConversationIDPayload) (contracts.AgentHistoryResult, error) {
			id, msgs, err := m.Service.OpenConversation(ctx, p.ID)
			if err != nil {
				return contracts.AgentHistoryResult{}, err
			}
			return historyResult(id, msgs), nil
		})

	realtime.Handle(r, contracts.ActionAgentDeleteConversation,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentConversationIDPayload) (contracts.Empty, error) {
			return contracts.Empty{}, m.Service.DeleteConversation(ctx, p.ID)
		})

	realtime.Handle(r, contracts.ActionAgentHistory,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentHistoryResult, error) {
			id, msgs, err := m.Service.History(ctx)
			if err != nil {
				return contracts.AgentHistoryResult{}, err
			}
			return historyResult(id, msgs), nil
		})

	realtime.Handle(r, contracts.ActionAgentListApprovals,
		func(_ context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentApprovalListResult, error) {
			return contracts.NewAgentApprovalListResult(m.Service.ListApprovals()), nil
		})

	realtime.Handle(r, contracts.ActionAgentResolveApproval,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentResolveApprovalPayload) (contracts.Empty, error) {
			_, err := m.Service.ResolveApproval(ctx, p.ID, p.Approve)
			return contracts.Empty{}, err
		})

	realtime.Handle(r, contracts.ActionAgentSetUIContext,
		func(_ context.Context, _ *realtime.Request, p contracts.AgentUIContextPayload) (contracts.Empty, error) {
			m.Service.SetUIContext(agentsvc.UIContext{
				Route: p.Route, Note: p.Note, Screen: p.Screen, Feature: p.Feature,
				SelectedFiles: p.SelectedFiles, Settings: p.Settings, LastResult: p.LastResult,
			})
			return contracts.Empty{}, nil
		})

	realtime.Handle(r, contracts.ActionAgentListAllowedDirs,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentAllowedDirsResult, error) {
			dirs, err := m.Service.ListAllowedDirs(ctx)
			if err != nil {
				return contracts.AgentAllowedDirsResult{}, err
			}
			return contracts.AgentAllowedDirsResult{Dirs: dirs}, nil
		})

	realtime.Handle(r, contracts.ActionAgentAddAllowedDir,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentAllowedDirPayload) (contracts.AgentAllowedDirsResult, error) {
			if err := m.trustedFoldersLocked(); err != nil {
				return contracts.AgentAllowedDirsResult{}, err
			}
			dirs, err := m.Service.AddAllowedDir(ctx, p.Dir)
			if err != nil {
				return contracts.AgentAllowedDirsResult{}, err
			}
			return contracts.AgentAllowedDirsResult{Dirs: dirs}, nil
		})

	realtime.Handle(r, contracts.ActionAgentRemoveAllowedDir,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentAllowedDirPayload) (contracts.AgentAllowedDirsResult, error) {
			if err := m.trustedFoldersLocked(); err != nil {
				return contracts.AgentAllowedDirsResult{}, err
			}
			dirs, err := m.Service.RemoveAllowedDir(ctx, p.Dir)
			if err != nil {
				return contracts.AgentAllowedDirsResult{}, err
			}
			return contracts.AgentAllowedDirsResult{Dirs: dirs}, nil
		})
}

func (m Agent) listResult(ctx context.Context) (contracts.AgentListResult, error) {
	list, err := m.Service.List(ctx)
	if err != nil {
		return contracts.AgentListResult{}, err
	}
	return toContractAgentList(list, m.Service.DeveloperMode()), nil
}

func historyResult(id string, msgs []domainagent.ChatMessage) contracts.AgentHistoryResult {
	out := make([]contracts.AgentChatMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = contracts.AgentChatMessage(msg)
	}
	return contracts.AgentHistoryResult{ConversationID: id, Messages: out}
}

func toContractAgentList(in []domainagent.ProviderInfo, developerMode bool) contracts.AgentListResult {
	items := make([]contracts.AgentProviderInfo, len(in))
	for i, it := range in {
		items[i] = contracts.NewAgentProviderInfo(it)
	}
	return contracts.AgentListResult{Providers: items, DeveloperMode: developerMode}
}
