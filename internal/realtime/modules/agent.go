package modules

import (
	"app/internal/apperr"
	"context"

	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	"app/internal/realtime"
	agentsvc "app/internal/service/agent"
)

// Realtime actions for agent provider link synergy.
const (
	ActionAgentLinkProvider        = "agent:link_provider"
	ActionAgentUnlinkProvider      = "agent:unlink_provider"
	ActionAgentListProviderLinks   = "agent:list_provider_links"
	ActionAgentCompatibleProviders = "agent:compatible_providers"
)

// Agent exposes agent:* actions over realtime WebSocket.
type Agent struct {
	Service *agentsvc.Service
	// LockTrustedFolders refuses agent:add_allowed_dir / agent:remove_allowed_dir. Set in headless
	// server mode: trusted folders also bound what remote clients may touch (the workflow file guard),
	// so a remote client must not be able to widen its own boundary.
	LockTrustedFolders bool
	// LockHostAdmin refuses agent:set_path in server mode unless server.allow_host_admin is enabled.
	LockHostAdmin bool
	// LockCustomizations refuses remote mutation of shared skills and subagents in server mode
	// unless server.allow_host_admin is enabled.
	LockCustomizations bool
}

func (m Agent) trustedFoldersLocked() error {
	if m.LockTrustedFolders {
		return apperr.Unavailable("trusted folders can only be changed in the desktop app on this machine")
	}
	return nil
}

func (m Agent) customizationsLocked() error {
	if m.LockCustomizations {
		return apperr.Unavailable("agent skills and subagents cannot be modified remotely in server mode")
	}
	return nil
}

func (Agent) Name() string { return "agent" }

func (m Agent) Register(r *realtime.Registry) {
	// Provider linking actions.
	realtime.Handle(r, ActionAgentLinkProvider,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentLinkProviderPayload) (contracts.Empty, error) {
			err := m.Service.SetProviderLink(ctx, domainagent.ProviderID(p.AgentProviderID), p.ProviderID, p.CredentialID)
			return contracts.Empty{}, err
		})

	realtime.Handle(r, ActionAgentUnlinkProvider,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.Empty, error) {
			err := m.Service.DeleteProviderLink(ctx, p.ID)
			return contracts.Empty{}, err
		})

	realtime.Handle(r, ActionAgentListProviderLinks,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (any, error) {
			links, err := m.Service.ListProviderLinks(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"links": links}, nil
		})

	realtime.Handle(r, ActionAgentCompatibleProviders,
		func(_ context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (any, error) {
			providers := m.Service.CompatibleProviders(p.ID)
			return map[string]any{"compatible_providers": providers}, nil
		})

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
			return m.toContractInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentSetPath,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSetPathPayload) (contracts.AgentProviderInfo, error) {
			if m.LockHostAdmin {
				return contracts.AgentProviderInfo{}, hostAdminLockedError()
			}
			info, err := m.Service.SetCustomPath(ctx, p.ID, p.CustomPath)
			if err != nil {
				return contracts.AgentProviderInfo{}, err
			}
			return m.toContractInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentLogin,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentLoginPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Login(ctx, p.ID, p.APIKey)
			if err != nil {
				return m.toContractInfo(info), err
			}
			return m.toContractInfo(info), nil
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
				return m.toContractInfo(info), err
			}
			return m.toContractInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentSetModel,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSetModelPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.SetModel(ctx, p.ID, p.Model, p.Effort)
			if err != nil {
				return contracts.AgentProviderInfo{}, err
			}
			return m.toContractInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionAgentConnect,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentProviderIDPayload) (contracts.AgentProviderInfo, error) {
			info, err := m.Service.Connect(ctx, p.ID)
			if err != nil {
				return m.toContractInfo(info), err
			}
			return m.toContractInfo(info), nil
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
			return contracts.Empty{}, m.Service.ResolveApproval(ctx, p.ID, p.Approve)
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

	realtime.Handle(r, contracts.ActionAgentListCustomizations,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentCustomizationsResult, error) {
			snap, err := m.Service.ListCustomizations(ctx)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentReloadCustomizations,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.AgentCustomizationsResult, error) {
			snap, err := m.Service.ReloadCustomizations(ctx)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentSaveSkill,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSaveSkillPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.SaveSkill(ctx, p.Name, p.Description, p.UserInvocable, p.Enabled, p.Body)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentDeleteSkill,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentCustomizationIDPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.DeleteSkill(ctx, p.Name)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentRestoreSkill,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentCustomizationIDPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.RestoreSkill(ctx, p.Name)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentSaveSubagent,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentSaveSubagentPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.SaveSubagent(ctx, domainagent.SubagentInfo{
				Name:        p.Name,
				Description: p.Description,
				Tools:       p.Tools,
				Skills:      p.Skills,
				Provider:    p.Provider,
				Model:       p.Model,
				Enabled:     p.Enabled,
				Body:        p.Body,
			})
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentDeleteSubagent,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentCustomizationIDPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.DeleteSubagent(ctx, p.Name)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})

	realtime.Handle(r, contracts.ActionAgentRestoreSubagent,
		func(ctx context.Context, _ *realtime.Request, p contracts.AgentCustomizationIDPayload) (contracts.AgentCustomizationsResult, error) {
			if err := m.customizationsLocked(); err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			snap, err := m.Service.RestoreSubagent(ctx, p.Name)
			if err != nil {
				return contracts.AgentCustomizationsResult{}, err
			}
			return contracts.NewAgentCustomizationsResult(snap), nil
		})
}

func (m Agent) listResult(ctx context.Context) (contracts.AgentListResult, error) {
	list, err := m.Service.List(ctx)
	if err != nil {
		return contracts.AgentListResult{}, err
	}
	return m.toContractAgentList(list, m.Service.DeveloperMode()), nil
}

func historyResult(id string, msgs []domainagent.ChatMessage) contracts.AgentHistoryResult {
	out := make([]contracts.AgentChatMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = contracts.AgentChatMessage(msg)
	}
	return contracts.AgentHistoryResult{ConversationID: id, Messages: out}
}

func (m Agent) toContractInfo(it domainagent.ProviderInfo) contracts.AgentProviderInfo {
	info := contracts.NewAgentProviderInfo(it)
	if m.Service != nil {
		info.CompatibleProviders = m.Service.CompatibleProviders(it.ID)
	}
	return info
}

func (m Agent) toContractAgentList(in []domainagent.ProviderInfo, developerMode bool) contracts.AgentListResult {
	items := make([]contracts.AgentProviderInfo, len(in))
	for i, it := range in {
		items[i] = m.toContractInfo(it)
	}
	return contracts.AgentListResult{Providers: items, DeveloperMode: developerMode}
}
