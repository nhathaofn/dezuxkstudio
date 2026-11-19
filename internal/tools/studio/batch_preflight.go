package studio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
	"app/internal/progress"
)

const (
	// usdRoundingScale rounds USD unit and batch estimates to 4 decimal places (0.01 cents).
	usdRoundingScale = 10_000.0
)

// NewBatchPreflightNode returns tool.batch_preflight for verifying providers, quotas, and USD cost before batch generation.
func NewBatchPreflightNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	maxItems := opts.StudioConfig.PreflightMaxItems
	warnCount := opts.StudioConfig.PreflightLargeBatchWarnCount
	allowedCaps := optionStrings(domainprovider.Capabilities())

	spec := engine.Spec{
		Type:        NodeTypeBatchPreflight,
		Provider:    studioProviderID,
		Label:       "Batch Preflight & Cost Estimator",
		Description: "Validates active provider credentials, model availability, unit pricing, and estimated wall-clock duration before running a generation batch.",
		Outputs: []engine.Port{
			{Name: "report", Label: "Preflight Report", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "project_id", Label: "Project ID", Kind: engine.ParamText},
			{Name: "episode_id", Label: "Episode ID", Kind: engine.ParamText},
			{Name: "capability", Label: "Capability", Kind: engine.ParamSelect, Options: allowedCaps, Default: string(domainprovider.CapabilityVideo)},
			{Name: "model_ref", Label: "Model", Kind: engine.ParamText},
			{Name: "item_count", Label: "Item Count", Kind: engine.ParamInteger, Min: 1, Max: maxItems, Default: "1"},
			{Name: "units_per_item", Label: "Units per Item", Kind: engine.ParamText},
		},
	}

	return engine.Typed(spec, func(ctx context.Context, in contracts.BatchPreflightInput) (contracts.BatchPreflightOutput, error) {
		capName := strings.ToLower(strings.TrimSpace(in.Capability))
		if capName == "" {
			capName = string(domainprovider.CapabilityVideo)
		}
		if !slices.Contains(allowedCaps, capName) {
			return contracts.BatchPreflightOutput{}, apperr.Validation(fmt.Sprintf("unsupported capability %q", capName)).
				WithMsg("errors.studio.invalid_capability", map[string]any{"capability": capName})
		}

		itemCount := in.ItemCount
		if itemCount == 0 {
			itemCount = 1
		}
		if itemCount < 1 || itemCount > maxItems {
			return contracts.BatchPreflightOutput{}, apperr.Validation(fmt.Sprintf("item_count must be between 1 and %d", maxItems)).
				WithMsg("errors.studio.invalid_item_count", map[string]any{"min": 1, "max": maxItems})
		}
		if math.IsNaN(in.UnitsPerItem) || math.IsInf(in.UnitsPerItem, 0) || in.UnitsPerItem < 0 {
			return contracts.BatchPreflightOutput{}, apperr.Validation("units_per_item must be a non-negative finite number").
				WithMsg("errors.studio.invalid_units_per_item", nil)
		}

		progress.Report(ctx, progress.Update{
			Percent: 20,
			Message: "Checking provider credentials and catalog pricing…",
			Key:     "studio.progress.batch_preflight",
		})

		capSpec := domainstudio.PreflightSpecForCapabilityWithDuration(capName, opts.StudioConfig.DefaultShotDurationSec)
		unit := capSpec.Unit
		unitsPerItem := in.UnitsPerItem
		if unitsPerItem <= 0 {
			unitsPerItem = capSpec.DefaultUnitsPerItem
		}

		out := contracts.BatchPreflightOutput{
			Ready:        true,
			Capability:   capName,
			ItemCount:    itemCount,
			Unit:         unit,
			Warnings:     []string{},
			WarningItems: []contracts.BatchPreflightMessage{},
			Blockers:     []string{},
			BlockerItems: []contracts.BatchPreflightMessage{},
		}
		addWarning := func(msg, key string, params map[string]string) {
			out.Warnings = append(out.Warnings, msg)
			out.WarningItems = append(out.WarningItems, contracts.BatchPreflightMessage{
				Message:   msg,
				MsgKey:    key,
				MsgParams: params,
			})
		}
		addBlocker := func(msg, key string, params map[string]string) {
			out.Ready = false
			out.Blockers = append(out.Blockers, msg)
			out.BlockerItems = append(out.BlockerItems, contracts.BatchPreflightMessage{
				Message:   msg,
				MsgKey:    key,
				MsgParams: params,
			})
		}

		if opts.ProviderChecker != nil {
			checkCtx, cancel := context.WithTimeout(ctx, opts.StudioConfig.FFprobeTimeout)
			stop := progress.KeepAlive(ctx)
			provID, modelID, err := opts.ProviderChecker.CheckCapability(checkCtx, domainprovider.Capability(capName), strings.TrimSpace(in.ModelRef))
			stop()
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return contracts.BatchPreflightOutput{}, ctx.Err()
				}
				var appErr *apperr.Error
				if errors.As(err, &appErr) && strings.TrimSpace(appErr.Message) != "" {
					var strParams map[string]string
					if rawParams, ok := appErr.Details["msg_params"].(map[string]any); ok && len(rawParams) > 0 {
						strParams = make(map[string]string, len(rawParams))
						for k, v := range rawParams {
							strParams[k] = fmt.Sprint(v)
						}
					}
					msgKey, _ := appErr.Details["msg_key"].(string)
					if msgKey == "" {
						msgKey = "studio.preflight.block.provider_unavailable"
					}
					addBlocker(appErr.Message, msgKey, strParams)
				} else {
					addBlocker(
						fmt.Sprintf("No active %s provider or model is ready", capName),
						"studio.preflight.block.provider_unavailable",
						map[string]string{"capability": capName},
					)
				}
			} else {
				out.ProviderID = provID
				out.ModelID = modelID
			}
		} else {
			addWarning("Provider checker unavailable; skipping credential verification", "studio.preflight.warn.checker_unavailable", nil)
		}

		unitPrice := capSpec.FallbackUnitPriceUSD
		usedWildcard := true
		if opts.CostEstimator != nil {
			p, wc, err := opts.CostEstimator.EstimateUnitPriceUSD(ctx, out.ProviderID, out.ModelID, capName, unit)
			if err != nil {
				if ctx.Err() != nil {
					return contracts.BatchPreflightOutput{}, ctx.Err()
				}
				if opts.Logger != nil {
					opts.Logger.WarnContext(ctx, "studio: pricing catalog estimate lookup failed; using fallback rate",
						"capability", capName,
						"error", err,
					)
				}
				addWarning("Pricing catalog lookup failed; using fallback rate", "studio.preflight.warn.fallback_rate", nil)
				usedWildcard = false
			} else if !math.IsNaN(p) && !math.IsInf(p, 0) && (p > 0 || (p == 0 && !wc)) {
				unitPrice = p
				usedWildcard = wc
			}
		}
		if usedWildcard {
			addWarning("Using fallback catalog rate for preflight estimate", "studio.preflight.warn.fallback_rate", nil)
		}
		if itemCount >= warnCount {
			addWarning(
				fmt.Sprintf("Large batch (%d items): review cost carefully before running", itemCount),
				"studio.preflight.warn.large_batch",
				map[string]string{"count": strconv.Itoa(itemCount)},
			)
		}

		out.UnitPriceUSD = math.Round(unitPrice*usdRoundingScale) / usdRoundingScale
		out.EstimatedUSD = math.Round(float64(itemCount)*unitsPerItem*unitPrice*usdRoundingScale) / usdRoundingScale
		workers := opts.StudioConfig.MaxConcurrentBatch
		batches := (itemCount + workers - 1) / workers
		out.EstimatedSec = batches * capSpec.EstimatedSecPerItem

		statusWord := "READY"
		if !out.Ready {
			statusWord = "BLOCKED"
		}
		out.Report = fmt.Sprintf(
			"Preflight %s: capability=%s items=%d estimated_usd=$%.4f estimated_sec=%ds",
			statusWord, out.Capability, out.ItemCount, out.EstimatedUSD, out.EstimatedSec,
		)

		progress.Report(ctx, progress.Update{
			Percent: 100,
			Message: "Preflight estimate complete",
			Key:     "studio.progress.done",
		})
		return out, nil
	})
}
