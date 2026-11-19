package mediagen

import (
	"context"

	domainmedia "app/internal/domain/mediagen"
)

func (s *Service) SetUsageHook(fn func(context.Context, domainmedia.UsageEvent)) {
	s.usageMu.Lock()
	s.onUsage = fn
	s.usageMu.Unlock()
}

func (s *Service) emitUsage(ctx context.Context, ev domainmedia.UsageEvent) {
	s.usageMu.RLock()
	fn := s.onUsage
	s.usageMu.RUnlock()
	if fn == nil {
		return
	}
	meta := domainmedia.UsageMetaFromContext(ctx)
	if ev.ProjectID == "" {
		ev.ProjectID = meta.ProjectID
	}
	if ev.EpisodeID == "" {
		ev.EpisodeID = meta.EpisodeID
	}
	if ev.ShotID == "" {
		ev.ShotID = meta.ShotID
	}
	if ev.JobID == "" {
		ev.JobID = meta.JobID
	}
	if ev.Operation == "" {
		ev.Operation = meta.Operation
	}
	if ev.EstimatedUSD == 0 {
		ev.EstimatedUSD = meta.EstimatedUSD
	}
	fn(ctx, ev)
}
