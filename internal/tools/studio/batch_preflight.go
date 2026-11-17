package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"app/internal/apperr"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
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

	unit, defaultUnits, secPerItem := preflightUnitForCapability(capName)
	unitsPerItem := in.UnitsPerItem
	if unitsPerItem <= 0 {
		unitsPerItem = defaultUnits
	}

	out := contracts.BatchPreflightOutput{
		Ready:      true,
		Capability: capName,
		ItemCount:  in.ItemCount,
		Unit:       unit,
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
		out.Warnings = append(out.Warnings, "Provider checker unavailable; skipping credential verification")
	}

	unitPrice := defaultFallbackUnitPrice(capName)
	usedWildcard := true
	if n.opts.CostEstimator != nil {
		p, wc, err := n.opts.CostEstimator.EstimateUnitPriceUSD(ctx, out.ProviderID, out.ModelID, capName, unit)
		if err == nil && p > 0 {
			unitPrice = p
			usedWildcard = wc
		}
	}
	if usedWildcard {
		out.Warnings = append(out.Warnings, "Using fallback catalog rate for preflight estimate")
	}
	if in.ItemCount >= 10 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Large batch (%d items): review cost carefully before running", in.ItemCount))
	}

	out.UnitPriceUSD = math.Round(unitPrice*10000) / 10000
	out.EstimatedUSD = math.Round(float64(in.ItemCount)*unitsPerItem*unitPrice*10000) / 10000
	workers := n.opts.StudioConfig.MaxConcurrentBatch
	if workers <= 0 {
		workers = 2
	}
	batches := (in.ItemCount + workers - 1) / workers
	out.EstimatedSec = batches * secPerItem
	return out, nil
}

func preflightUnitForCapability(capName string) (unit string, unitsPerItem float64, secPerItem int) {
	switch domainprovider.Capability(capName) {
	case domainprovider.CapabilityLLM:
		return "1k_tokens", 2.0, 4
	case domainprovider.CapabilityImage:
		return "image", 1.0, 8
	case domainprovider.CapabilityVideo:
		return "second", 5.0, 35
	case domainprovider.CapabilityTTS:
		return "1k_chars", 0.5, 4
	case domainprovider.CapabilityLipSync:
		return "second", 5.0, 45
	default:
		return "request", 1.0, 10
	}
}

func defaultFallbackUnitPrice(capName string) float64 {
	switch domainprovider.Capability(capName) {
	case domainprovider.CapabilityLLM:
		return 0.005
	case domainprovider.CapabilityImage:
		return 0.04
	case domainprovider.CapabilityVideo:
		return 0.08
	case domainprovider.CapabilityTTS:
		return 0.015
	case domainprovider.CapabilityLipSync:
		return 0.12
	default:
		return 0.01
	}
}
