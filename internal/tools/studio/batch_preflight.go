package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	"app/internal/engine"
)

type batchPreflightNode struct {
	opts Options
}

// NewBatchPreflightNode returns tool.batch_preflight for verifying providers, quotas, and USD cost before batch generation.
func NewBatchPreflightNode(opts Options) engine.Node {
	opts.StudioConfig = withStudioDefaults(opts.StudioConfig)
	return &batchPreflightNode{opts: opts}
}

func (n *batchPreflightNode) Spec() engine.Spec {
	return engine.Spec{
		Type:        NodeTypeBatchPreflight,
		Provider:    "studio",
		Label:       "Batch Preflight & Cost Estimator",
		Description: "Validates active provider credentials, model availability, unit pricing, and estimated wall-clock duration before running a generation batch.",
		Outputs: []engine.Port{
			{Name: "report", Label: "Preflight Report", Type: engine.PortText},
		},
		Params: []engine.Param{
			{Name: "project_id", Label: "Project ID", Kind: engine.ParamText},
			{Name: "episode_id", Label: "Episode ID", Kind: engine.ParamText},
			{Name: "capability", Label: "Capability", Kind: engine.ParamSelect, Options: optionStrings(domainprovider.Capabilities()), Default: string(domainprovider.CapabilityVideo)},
			{Name: "model_ref", Label: "Model", Kind: engine.ParamText},
			{Name: "item_count", Label: "Item Count", Kind: engine.ParamInteger, Min: 1, Max: 500, Default: "1"},
		},
	}
}

func (n *batchPreflightNode) Run(ctx context.Context, raw json.RawMessage) (any, error) {
	var in contracts.BatchPreflightInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, apperr.Validation("invalid batch_preflight input").WithDetails(map[string]any{"cause": err.Error()})
		}
	}
	capName := strings.ToLower(strings.TrimSpace(in.Capability))
	if capName == "" {
		capName = string(domainprovider.CapabilityVideo)
	}
	if in.ItemCount <= 0 {
		in.ItemCount = 1
	}

	spec := domainstudio.PreflightSpecForCapability(capName)
	unit := spec.Unit
	unitsPerItem := in.UnitsPerItem
	if unitsPerItem <= 0 {
		unitsPerItem = spec.DefaultUnitsPerItem
	}

	out := contracts.BatchPreflightOutput{
		Ready:        true,
		Capability:   capName,
		ItemCount:    in.ItemCount,
		Unit:         unit,
		Warnings:     []string{},
		WarningItems: []contracts.BatchPreflightMessage{},
		Blockers:     []string{},
	}
	addWarning := func(msg, key string, params map[string]string) {
		out.Warnings = append(out.Warnings, msg)
		out.WarningItems = append(out.WarningItems, contracts.BatchPreflightMessage{
			Message:   msg,
			MsgKey:    key,
			MsgParams: params,
		})
	}

	if n.opts.ProviderChecker != nil {
		provID, modelID, err := n.opts.ProviderChecker.CheckCapability(ctx, domainprovider.Capability(capName), strings.TrimSpace(in.ModelRef))
		if err != nil {
			out.Ready = false
			out.Blockers = append(out.Blockers, err.Error())
		} else {
			out.ProviderID = provID
			out.ModelID = modelID
		}
	} else {
		addWarning("Provider checker unavailable; skipping credential verification", "studio.preflight.warn.checker_unavailable", nil)
	}

	unitPrice := spec.FallbackUnitPriceUSD
	usedWildcard := true
	if n.opts.CostEstimator != nil {
		p, wc, err := n.opts.CostEstimator.EstimateUnitPriceUSD(ctx, out.ProviderID, out.ModelID, capName, unit)
		if err == nil && p > 0 {
			unitPrice = p
			usedWildcard = wc
		}
	}
	if usedWildcard {
		addWarning("Using fallback catalog rate for preflight estimate", "studio.preflight.warn.fallback_rate", nil)
	}
	if in.ItemCount >= 10 {
		addWarning(fmt.Sprintf("Large batch (%d items): review cost carefully before running", in.ItemCount), "studio.preflight.warn.large_batch", map[string]string{"count": strconv.Itoa(in.ItemCount)})
	}

	out.UnitPriceUSD = math.Round(unitPrice*10000) / 10000
	out.EstimatedUSD = math.Round(float64(in.ItemCount)*unitsPerItem*unitPrice*10000) / 10000
	workers := withStudioDefaults(n.opts.StudioConfig).MaxConcurrentBatch
	batches := (in.ItemCount + workers - 1) / workers
	out.EstimatedSec = batches * spec.EstimatedSecPerItem
	return out, nil
}
