// Package provider exposes API-provider management (LLM / image / video / TTS keys) as workflow nodes,
// so the UI workflows and the agent use it like any other feature (see docs/agent-feature-integration.md).
//
// Every setting is derived from the provider registry (internal/domain/provider): adding a provider or a
// credential field there updates these nodes with no change here.
package provider

import (
	"context"
	"fmt"
	"strings"

	domain "app/internal/domain/provider"
	"app/internal/engine"
)

// Node types.
const (
	NodeList     = "tool.provider_list"
	NodeSetup    = "tool.provider_setup"
	NodeTest     = "tool.provider_test"
	NodeActivate = "tool.provider_activate"
	NodeDelete   = "tool.provider_delete"
)

// Option sources (engine.Param.OptionsFrom).
const (
	OptionsSaved       = "provider.saved"       // saved credentials and custom providers
	OptionsCredentials = "provider.credentials" // saved credentials of built-in providers
)

// Auto lets setup detect the provider / protocol.
const Auto = "auto"

// Service is what the nodes need from internal/service/provider.
type Service interface {
	Overview(ctx context.Context) (any, error)
	Setup(ctx context.Context, req domain.SetupRequest) (any, error)
	TestByID(ctx context.Context, id string) (any, error)
	ActivateCredential(ctx context.Context, id string) error
	DeleteByID(ctx context.Context, id string) error
	Describe(ctx context.Context, id string) (string, error)
	SavedOptions(ctx context.Context, credentialsOnly bool) ([]engine.Option, error)
}

// Input is a node's settings; setup fields are dynamic (one per registry field), so the input is a map.
type Input map[string]any

func (in Input) str(name string) string {
	switch v := in[name].(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func (in Input) flag(name string) bool { v, _ := in[name].(bool); return v }

// Nodes builds every provider node.
func Nodes(svc Service) []engine.Node {
	return []engine.Node{listNode(svc), setupNode(svc), testNode(svc), activateNode(svc), deleteNode(svc)}
}

// OptionSources returns the option sources the nodes read.
func OptionSources(svc Service) map[string]engine.OptionSource {
	return map[string]engine.OptionSource{
		OptionsSaved: func(ctx context.Context, _ map[string]string) ([]engine.Option, error) {
			return svc.SavedOptions(ctx, false)
		},
		OptionsCredentials: func(ctx context.Context, _ map[string]string) ([]engine.Option, error) {
			return svc.SavedOptions(ctx, true)
		},
	}
}

func listNode(svc Service) engine.Node {
	return engine.Typed(engine.Spec{
		Type: NodeList, Provider: "tool", Label: "API Providers",
		Description: "List AI API providers (LLM, image, video, TTS): which are configured, the fields each needs, " +
			"saved credentials with masked keys and their last test result (dead keys are flagged).",
	}, func(ctx context.Context, _ Input) (any, error) { return svc.Overview(ctx) })
}

// fixedSetupParams is the number of setup params before the registry-derived credential fields.
var fixedSetupParams int

// setupParams derives the setup settings from the registry: provider ids, protocols and every
// credential field any provider declares.
func setupParams() []engine.Param {
	ids := []string{Auto}
	for _, m := range domain.Registry() {
		ids = append(ids, m.ID)
	}
	formats := []string{Auto}
	for _, f := range domain.DiscoveryFormats() {
		formats = append(formats, string(f))
	}
	params := []engine.Param{
		{Name: "provider", Label: "Provider", Kind: engine.ParamSelect, Options: ids, Default: Auto},
		{Name: domain.FieldAPIKey, Label: "API key", Kind: engine.ParamText, Secret: true},
		{Name: domain.FieldBaseURL, Label: "Base URL (empty = provider default)", Kind: engine.ParamText},
		{Name: "name", Label: "Name (optional)", Kind: engine.ParamText},
		{Name: "protocol", Label: "Protocol of a custom endpoint", Kind: engine.ParamSelect, Options: formats, Default: Auto},
	}
	fixedSetupParams = len(params)
	seen := map[string]bool{domain.FieldAPIKey: true, domain.FieldBaseURL: true}
	for _, m := range domain.Registry() {
		for _, f := range m.Fields {
			if seen[f.Key] {
				continue
			}
			seen[f.Key] = true
			label := strings.ReplaceAll(f.Key, "_", " ")
			if f.Kind == domain.FieldFile {
				label += " (file contents)"
			}
			params = append(params, engine.Param{Name: f.Key, Label: label, Kind: engine.ParamText, Secret: f.Secret})
		}
	}
	return params
}

func setupNode(svc Service) engine.Node {
	params := setupParams()
	return engine.Typed(engine.Spec{
		Type: NodeSetup, Provider: "tool", Label: "Set up API key",
		Description: "Save an AI provider API key the user gave, make it active and test it. provider=auto detects " +
			"the built-in provider from base_url or the key prefix; an unknown base_url becomes a custom " +
			"OpenAI/Gemini/Anthropic-compatible provider with its models discovered (nothing is saved if discovery " +
			"fails). Extra fields: access_key+secret_key for Kling, credentials_file (service-account JSON) for Vertex.",
		Params: params,
	}, func(ctx context.Context, in Input) (any, error) {
		req := domain.SetupRequest{APIKey: in.str(domain.FieldAPIKey), BaseURL: in.str(domain.FieldBaseURL),
			Name: in.str("name"), Secrets: map[string]string{}, Values: map[string]string{}}
		if p := in.str("provider"); p != Auto {
			req.ProviderID = p
		}
		if f := in.str("protocol"); f != Auto {
			req.DiscoveryFormat = domain.DiscoveryFormat(f)
		}
		for _, p := range params[fixedSetupParams:] { // registry-derived fields
			v := in.str(p.Name)
			if v == "" {
				continue
			}
			if p.Secret {
				req.Secrets[p.Name] = v
			} else {
				req.Values[p.Name] = v
			}
		}
		if req.APIKey == "" && len(req.Secrets) == 0 {
			return nil, engine.Invalid("api_key (or the provider's secret fields) is required")
		}
		return svc.Setup(ctx, req)
	})
}

func idParam(source string) engine.Param {
	return engine.Param{Name: "id", Label: "Saved key", Kind: engine.ParamSelect, OptionsFrom: source}
}

func requireID(in Input) (string, error) {
	id := in.str("id")
	if id == "" {
		return "", engine.Invalid("id is required (see " + NodeList + ")")
	}
	return id, nil
}

func testNode(svc Service) engine.Node {
	return engine.Typed(engine.Spec{
		Type: NodeTest, Provider: "tool", Label: "Test API key",
		Description: "Test a saved provider credential or custom provider against its API and record the result.",
		Params:      []engine.Param{idParam(OptionsSaved)},
	}, func(ctx context.Context, in Input) (any, error) {
		id, err := requireID(in)
		if err != nil {
			return nil, err
		}
		return svc.TestByID(ctx, id)
	})
}

func activateNode(svc Service) engine.Node {
	return engine.Typed(engine.Spec{
		Type: NodeActivate, Provider: "tool", Label: "Use API key",
		Description: "Make a saved credential the active (used) key of its built-in provider.",
		Params:      []engine.Param{idParam(OptionsCredentials)},
	}, func(ctx context.Context, in Input) (any, error) {
		id, err := requireID(in)
		if err != nil {
			return nil, err
		}
		if err := svc.ActivateCredential(ctx, id); err != nil {
			return nil, err
		}
		name, _ := svc.Describe(ctx, id)
		return map[string]any{"ok": true, "active": name}, nil
	})
}

func deleteNode(svc Service) engine.Node {
	return engine.Typed(engine.Spec{
		Type: NodeDelete, Provider: "tool", Label: "Delete API key",
		Description: "Delete a saved credential or custom provider and its stored key. Requires confirm=true, " +
			"which waits for the user's approval.",
		Params: []engine.Param{idParam(OptionsSaved),
			{Name: "confirm", Label: "Delete the key", Kind: engine.ParamToggle, Default: "false", Destructive: true}},
	}, func(ctx context.Context, in Input) (any, error) {
		id, err := requireID(in)
		if err != nil {
			return nil, err
		}
		if !in.flag("confirm") {
			return nil, engine.Invalid("set confirm=true to delete")
		}
		name, err := svc.Describe(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := svc.DeleteByID(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "deleted": name}, nil
	})
}
