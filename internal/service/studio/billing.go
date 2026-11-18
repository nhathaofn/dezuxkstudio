package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainmedia "app/internal/domain/mediagen"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	studionodes "app/internal/tools/studio"
)

func isHeavyGenerationStage(stage contracts.StudioRunStageAction) bool {
	switch stage {
	case contracts.StudioStageGenerateAssets,
		contracts.StudioStageGenerateKeyframes,
		contracts.StudioStageGenerateVideos,
		contracts.StudioStageGenerateTTS:
		return true
	default:
		return false
	}
}

// HandleMediaUsage records completed provider consumption into studio_cost_ledger
// whenever mediagen generates text, images, video, TTS, or lip-sync media.
func (s *Service) HandleMediaUsage(ctx context.Context, ev domainmedia.UsageEvent) {
	if s == nil || s.repo == nil || strings.TrimSpace(ev.ProviderID) == "" {
		return
	}
	unit := strings.TrimSpace(ev.Unit)
	if unit == "" {
		unit = "request"
	}
	actual := ev.ActualUSD
	est := ev.EstimatedUSD
	if actual == 0 {
		if price, ok, err := s.repo.ResolveUnitPrice(ctx, ev.ProviderID, ev.ModelID, ev.Capability, unit); err == nil && ok {
			units := ev.OutputUnits
			if ev.Capability == string(domainprovider.CapabilityLLM) {
				units = ev.InputUnits + ev.OutputUnits
			} else if units <= 0 {
				units = ev.InputUnits
			}
			if units <= 0 {
				units = 1
			}
			actual = math.Round(units*price.UnitPriceUSD*10000) / 10000
		}
	}
	if est == 0 {
		est = actual
	}

	status := domainstudio.CostRecorded
	if ev.Failed {
		status = domainstudio.CostFailed
	}
	_, err := s.repo.RecordCost(ctx, domainstudio.CostLedgerEntry{
		ProjectID:    ev.ProjectID,
		EpisodeID:    ev.EpisodeID,
		ShotID:       ev.ShotID,
		JobID:        ev.JobID,
		ProviderID:   ev.ProviderID,
		ModelID:      ev.ModelID,
		Capability:   ev.Capability,
		Operation:    ev.Operation,
		InputUnits:   ev.InputUnits,
		OutputUnits:  ev.OutputUnits,
		Unit:         unit,
		EstimatedUSD: est,
		ActualUSD:    actual,
		Status:       status,
	})
	if err != nil {
		s.log.WarnContext(ctx, "studio: failed to record cost ledger entry", "error", err)
		return
	}

	if ev.JobID != "" && actual > 0 {
		if job, jerr := s.repo.GetJob(ctx, ev.JobID); jerr == nil {
			job.ActualUSD = actual
			if _, uerr := s.repo.UpdateJob(ctx, job); uerr != nil {
				s.log.WarnContext(ctx, "studio: failed to update job actual cost", "id", ev.JobID, "error", uerr)
			}
		}
	}

	if ev.ProjectID != "" {
		if sum, serr := s.repo.SummarizeCosts(ctx, ev.ProjectID, ev.EpisodeID); serr == nil {
			s.emit(contracts.EventStudioCostUpdated, contracts.StudioCostUpdatedEvent{
				ProjectID:         ev.ProjectID,
				EpisodeID:         ev.EpisodeID,
				ActualTotalUSD:    sum.ActualTotalUSD,
				EstimatedTotalUSD: sum.EstimatedTotalUSD,
				EntryCount:        sum.EntryCount,
			})
		}
	}
}

// EstimateUnitPriceUSD satisfies studionodes.CostEstimator using studio_pricing_catalog.
func (s *Service) EstimateUnitPriceUSD(ctx context.Context, providerID, modelID, capability, unit string) (float64, bool, error) {
	if s.repo == nil {
		return 0, true, nil
	}
	entry, ok, err := s.repo.ResolveUnitPrice(ctx, providerID, modelID, capability, unit)
	if err != nil || !ok {
		return 0, true, err
	}
	wildcard := entry.ProviderID == "*" || entry.ModelID == "*"
	return entry.UnitPriceUSD, wildcard, nil
}

// GetCostSummary returns cost breakdown totals, ledger entries, and catalog rates for a project/episode.
func (s *Service) GetCostSummary(ctx context.Context, p contracts.StudioCostSummaryPayload) (contracts.StudioCostSummaryResult, error) {
	if s.repo == nil {
		return contracts.StudioCostSummaryResult{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioCostSummaryResult{}, err
	}
	sum, err := s.repo.SummarizeCosts(ctx, p.ProjectID, p.EpisodeID)
	if err != nil {
		return contracts.StudioCostSummaryResult{}, err
	}
	entries, err := s.repo.ListCosts(ctx, domainstudio.CostFilter{
		ProjectID: p.ProjectID,
		EpisodeID: p.EpisodeID,
		Limit:     p.Limit,
	})
	if err != nil {
		return contracts.StudioCostSummaryResult{}, err
	}
	pricing, err := s.repo.ListPricing(ctx)
	if err != nil {
		return contracts.StudioCostSummaryResult{}, err
	}
	outEntries := make([]contracts.StudioCostLedgerEntry, len(entries))
	for i := range entries {
		outEntries[i] = costLedgerToDTO(entries[i])
	}
	outPricing := make([]contracts.StudioPricingEntry, len(pricing))
	for i := range pricing {
		outPricing[i] = pricingToDTO(pricing[i])
	}
	byCap := sum.ByCapability
	if byCap == nil {
		byCap = map[string]float64{}
	}
	byProv := sum.ByProvider
	if byProv == nil {
		byProv = map[string]float64{}
	}
	return contracts.StudioCostSummaryResult{
		ProjectID:         sum.ProjectID,
		EpisodeID:         sum.EpisodeID,
		Currency:          sum.Currency,
		EstimatedTotalUSD: math.Round(sum.EstimatedTotalUSD*10000) / 10000,
		ActualTotalUSD:    math.Round(sum.ActualTotalUSD*10000) / 10000,
		EntryCount:        sum.EntryCount,
		ByCapability:      byCap,
		ByProvider:        byProv,
		Entries:           outEntries,
		Pricing:           outPricing,
	}, nil
}

// UpsertPricing adds or updates a provider/model rate in studio_pricing_catalog.
func (s *Service) UpsertPricing(ctx context.Context, p contracts.StudioPricingUpsertPayload) (contracts.StudioPricingEntry, error) {
	if s.repo == nil {
		return contracts.StudioPricingEntry{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.StudioPricingEntry{}, err
	}
	saved, err := s.repo.UpsertPricing(ctx, domainstudio.PricingEntry{
		ProviderID:   p.ProviderID,
		ModelID:      p.ModelID,
		Capability:   p.Capability,
		Unit:         p.Unit,
		UnitPriceUSD: p.UnitPriceUSD,
	})
	if err != nil {
		return contracts.StudioPricingEntry{}, err
	}
	return pricingToDTO(saved), nil
}

// RunBatchPreflight validates providers & estimates USD spend before executing a stage or enqueueing jobs.
func (s *Service) RunBatchPreflight(ctx context.Context, p contracts.StudioBatchPreflightPayload) (contracts.BatchPreflightOutput, error) {
	if s.repo == nil {
		return contracts.BatchPreflightOutput{}, apperr.Unavailable("studio repository unavailable").
			WithMsg("errors.studio.service_unavailable", nil)
	}
	if err := p.Validate(); err != nil {
		return contracts.BatchPreflightOutput{}, err
	}
	proj, err := s.repo.GetProject(ctx, strings.TrimSpace(p.ProjectID))
	if err != nil {
		return contracts.BatchPreflightOutput{}, err
	}
	capName, modelRef, itemCount, unitsPerItem := s.resolvePreflightParams(ctx, proj, p)
	in := contracts.BatchPreflightInput{
		ProjectID:    proj.ID,
		EpisodeID:    p.EpisodeID,
		ModelRef:     modelRef,
		Capability:   capName,
		ItemCount:    itemCount,
		UnitsPerItem: unitsPerItem,
	}
	if s.wf != nil {
		inBytes, merr := json.Marshal(in)
		if merr != nil {
			return contracts.BatchPreflightOutput{}, apperr.Internal(merr)
		}
		rawOut, rerr := s.wf.RunNode(ctx, studionodes.NodeTypeBatchPreflight, inBytes)
		if rerr != nil {
			return contracts.BatchPreflightOutput{}, rerr
		}
		var out contracts.BatchPreflightOutput
		if derr := decodeNodeOutput(rawOut, &out); derr != nil {
			return contracts.BatchPreflightOutput{}, derr
		}
		if out.Warnings == nil {
			out.Warnings = []string{}
		}
		if out.WarningItems == nil {
			out.WarningItems = []contracts.BatchPreflightMessage{}
		}
		if out.Blockers == nil {
			out.Blockers = []string{}
		}
		if out.BlockerItems == nil {
			out.BlockerItems = []contracts.BatchPreflightMessage{}
		}
		return out, nil
	}

	spec := domainstudio.PreflightSpecForCapabilityWithDuration(capName, s.cfg.DefaultShotDurationSec)
	provID, modelID := domainstudio.SplitModelRefForPricing(modelRef)
	price := spec.FallbackUnitPriceUSD
	if entry, ok, rerr := s.repo.ResolveUnitPrice(ctx, provID, modelID, capName, spec.Unit); rerr != nil {
		s.log.WarnContext(ctx, "studio: failed to resolve unit price in preflight fallback", "error", rerr)
	} else if ok {
		price = entry.UnitPriceUSD
	}
	workers := max(1, s.cfg.MaxConcurrentBatch)
	batches := (itemCount + workers - 1) / workers
	estUSD := math.Round(float64(itemCount)*unitsPerItem*price*10000) / 10000
	return contracts.BatchPreflightOutput{
		Report:       fmt.Sprintf("Preflight READY: capability=%s items=%d estimated_usd=$%.4f", capName, itemCount, estUSD),
		Ready:        true,
		ProviderID:   provID,
		ModelID:      modelID,
		Capability:   capName,
		ItemCount:    itemCount,
		Unit:         spec.Unit,
		UnitPriceUSD: math.Round(price*10000) / 10000,
		EstimatedUSD: estUSD,
		EstimatedSec: batches * spec.EstimatedSecPerItem,
		Warnings:     []string{},
		WarningItems: []contracts.BatchPreflightMessage{},
		Blockers:     []string{},
		BlockerItems: []contracts.BatchPreflightMessage{},
	}, nil
}

func (s *Service) resolvePreflightParams(ctx context.Context, proj domainstudio.Project, p contracts.StudioBatchPreflightPayload) (capName, modelRef string, itemCount int, unitsPerItem float64) {
	itemCount = p.ItemCount
	switch strings.ToLower(strings.TrimSpace(p.Stage)) {
	case "generate_assets":
		capName = string(domainprovider.CapabilityImage)
		modelRef = firstNonEmpty(p.Model, proj.DefaultImageModel)
		unitsPerItem = domainstudio.PreflightSpecForCapabilityWithDuration(capName, s.cfg.DefaultShotDurationSec).DefaultUnitsPerItem
		if itemCount <= 0 {
			assets, err := s.repo.ListAssets(ctx, proj.ID)
			if err != nil {
				s.log.WarnContext(ctx, "studio: failed to list assets for preflight", "project_id", proj.ID, "error", err)
			}
			itemCount = len(assets)
		}
	case "generate_keyframes":
		capName = string(domainprovider.CapabilityImage)
		modelRef = firstNonEmpty(p.Model, proj.DefaultImageModel)
		unitsPerItem = domainstudio.PreflightSpecForCapabilityWithDuration(capName, s.cfg.DefaultShotDurationSec).DefaultUnitsPerItem
		if itemCount <= 0 && p.EpisodeID != "" {
			shots, err := s.repo.ListShots(ctx, p.EpisodeID)
			if err != nil {
				s.log.WarnContext(ctx, "studio: failed to list shots for preflight", "episode_id", p.EpisodeID, "error", err)
			}
			itemCount = len(shots)
		}
	case "generate_videos":
		capName = string(domainprovider.CapabilityVideo)
		modelRef = firstNonEmpty(p.Model, proj.DefaultVideoModel)
		unitsPerItem = float64(max(1, s.cfg.DefaultShotDurationSec))
		if p.EpisodeID != "" {
			shots, err := s.repo.ListShots(ctx, p.EpisodeID)
			if err != nil {
				s.log.WarnContext(ctx, "studio: failed to list shots for video preflight", "episode_id", p.EpisodeID, "error", err)
			}
			if itemCount <= 0 {
				itemCount = len(shots)
			}
			if len(shots) > 0 {
				tot := 0
				for _, sh := range shots {
					tot += max(1, sh.DurationSec)
				}
				unitsPerItem = float64(tot) / float64(len(shots))
			}
		}
	case "generate_tts":
		capName = string(domainprovider.CapabilityTTS)
		modelRef = firstNonEmpty(p.Model, proj.DefaultTTSModel)
		unitsPerItem = domainstudio.PreflightSpecForCapabilityWithDuration(capName, s.cfg.DefaultShotDurationSec).DefaultUnitsPerItem
		if itemCount <= 0 && p.EpisodeID != "" {
			shots, err := s.repo.ListShots(ctx, p.EpisodeID)
			if err != nil {
				s.log.WarnContext(ctx, "studio: failed to list shots for tts preflight", "episode_id", p.EpisodeID, "error", err)
			}
			itemCount = len(shots)
		}
	case "lipsync":
		capName = string(domainprovider.CapabilityLipSync)
		modelRef = p.Model
		unitsPerItem = float64(max(1, s.cfg.DefaultShotDurationSec))
		if itemCount <= 0 && p.EpisodeID != "" {
			shots, err := s.repo.ListShots(ctx, p.EpisodeID)
			if err != nil {
				s.log.WarnContext(ctx, "studio: failed to list shots for lipsync preflight", "episode_id", p.EpisodeID, "error", err)
			}
			itemCount = len(shots)
		}
	default:
		capName = string(domainprovider.CapabilityLLM)
		modelRef = firstNonEmpty(p.Model, proj.DefaultTextModel)
		unitsPerItem = domainstudio.PreflightSpecForCapabilityWithDuration(capName, s.cfg.DefaultShotDurationSec).DefaultUnitsPerItem
	}
	if p.UnitsPerItem > 0 {
		unitsPerItem = p.UnitsPerItem
	}
	if itemCount <= 0 {
		itemCount = 1
	}
	return capName, modelRef, itemCount, unitsPerItem
}
