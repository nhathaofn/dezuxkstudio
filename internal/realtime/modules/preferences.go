package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/realtime"
	"app/internal/service/preferences"
)

// Preferences exposes preferences:* actions.
type Preferences struct {
	Service *preferences.Service
}

func (Preferences) Name() string { return "preferences" }

func (m Preferences) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionPreferencesGet,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.Preferences, error) {
			p, err := m.Service.Get(ctx)
			return contracts.NewPreferences(p), err
		})

	realtime.Handle(r, contracts.ActionPreferencesUpdate,
		func(ctx context.Context, _ *realtime.Request, patch contracts.PreferencesPatch) (contracts.Preferences, error) {
			p, err := m.Service.Update(ctx, patch.ToDomain())
			return contracts.NewPreferences(p), err
		})
}
