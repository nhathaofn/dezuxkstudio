package modules

import (
	"context"

	"app/internal/contracts"
	"app/internal/domain/binaries"
	"app/internal/realtime"
	binariessvc "app/internal/service/binaries"
)

// Binaries exposes binaries:* actions over realtime WebSocket.
type Binaries struct {
	Service *binariessvc.Service
}

func (Binaries) Name() string { return "binaries" }

func (m Binaries) Register(r *realtime.Registry) {
	realtime.Handle(r, contracts.ActionBinariesList,
		func(ctx context.Context, _ *realtime.Request, _ contracts.Empty) (contracts.BinaryListResult, error) {
			list, err := m.Service.List(ctx)
			if err != nil {
				return contracts.BinaryListResult{}, err
			}
			items := make([]contracts.BinaryInfo, len(list))
			for i, it := range list {
				items[i] = toContractBinaryInfo(it)
			}
			return contracts.BinaryListResult{Binaries: items}, nil
		})

	realtime.Handle(r, contracts.ActionBinariesProbe,
		func(ctx context.Context, _ *realtime.Request, p contracts.BinaryIDPayload) (contracts.BinaryInfo, error) {
			info, err := m.Service.Probe(ctx, p.ID)
			if err != nil {
				return contracts.BinaryInfo{}, err
			}
			return toContractBinaryInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionBinariesSetPath,
		func(ctx context.Context, _ *realtime.Request, p contracts.BinarySetPathPayload) (contracts.BinaryInfo, error) {
			info, err := m.Service.SetCustomPath(ctx, p.ID, p.CustomPath)
			if err != nil {
				return contracts.BinaryInfo{}, err
			}
			return toContractBinaryInfo(info), nil
		})

	realtime.Handle(r, contracts.ActionBinariesDownload,
		func(ctx context.Context, req *realtime.Request, p contracts.BinaryIDPayload) (contracts.BinaryInfo, error) {
			info, err := m.Service.Download(ctx, p.ID, req.Progress)
			if err != nil {
				return contracts.BinaryInfo{}, err
			}
			return toContractBinaryInfo(info), nil
		})
}

func toContractBinaryInfo(it binaries.BinaryInfo) contracts.BinaryInfo {
	return contracts.BinaryInfo{
		ID:           it.ID,
		Name:         it.Name,
		Description:  it.Description,
		Executable:   it.Executable,
		ResolvedPath: it.ResolvedPath,
		CustomPath:   it.CustomPath,
		Source:       it.Source,
		Version:      it.Version,
		Status:       it.Status,
		CanDownload:  it.CanDownload,
	}
}
