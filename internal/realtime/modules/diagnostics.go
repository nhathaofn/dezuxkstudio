package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/realtime"
	"app/internal/service/diagnostics"
)

// Diagnostics exposes diagnostics:* actions. Export is the reference implementation
// of a long-running action: it reports progress and stops when the client cancels.
type Diagnostics struct {
	Service *diagnostics.Service
}

func (Diagnostics) Name() string { return "diagnostics" }

func (m Diagnostics) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionDiagnosticsExport,
		func(ctx context.Context, req *realtime.Request, _ contracts.Empty) (contracts.DiagnosticsBundle, error) {
			return m.Service.Export(ctx, req.Progress)
		})
}
