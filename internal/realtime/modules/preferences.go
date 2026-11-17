package modules

import (
	"context"

	"app/internal/apperr"
	"app/internal/contracts"
	"app/internal/realtime"
	"app/internal/service/preferences"
)

// Preferences exposes preferences:* actions.
type Preferences struct {
	Service            *preferences.Service
	LockTrustedFolders bool
}

func (Preferences) Name() string { return "preferences" }

func (m Preferences) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionPreferencesGet,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.Preferences, error) {
			p, err := m.Service.Get(ctx)
			if err != nil {
				return contracts.Preferences{}, err
			}
			return contracts.NewPreferences(p), nil
		})

	realtime.Handle(r, contracts.ActionPreferencesUpdate,
		func(ctx context.Context, _ *realtime.Request, patch contracts.PreferencesPatch) (contracts.Preferences, error) {
			if m.LockTrustedFolders && (patch.WorkspaceRoot != nil || patch.OnboardingCompleted != nil || patch.AgentSkipTrustedApproval != nil) {
				return contracts.Preferences{}, apperr.Unavailable("trusted folders and workspace settings can only be changed in the desktop app on this machine")
			}
			p, err := m.Service.Update(ctx, patch.ToDomain())
			if err != nil {
				return contracts.Preferences{}, err
			}
			return contracts.NewPreferences(p), nil
		})
}
