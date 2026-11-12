package workflow_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	domain "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/paths"
	"app/internal/service/workflow"
)

type fakeProviders struct{}

func (fakeProviders) Overview(context.Context) (any, error)                   { return nil, nil }
func (fakeProviders) Setup(context.Context, domain.SetupRequest) (any, error) { return nil, nil }
func (fakeProviders) TestByID(context.Context, string) (any, error)           { return nil, nil }
func (fakeProviders) ActivateCredential(context.Context, string) error        { return nil }
func (fakeProviders) DeleteByID(context.Context, string) error                { return nil }
func (fakeProviders) Describe(context.Context, string) (string, error)        { return "", nil }
func (fakeProviders) SavedOptions(context.Context, bool) ([]engine.Option, error) {
	return []engine.Option{{Value: "c1", Label: "OpenAI"}}, nil
}

func (fakeProviders) OwnerOptions(context.Context) ([]engine.Option, error) {
	return []engine.Option{{Value: "openai", Label: "OpenAI"}}, nil
}
func (fakeProviders) ModelOptions(context.Context, domain.Purpose) ([]engine.Option, error) {
	return []engine.Option{{Value: "openai/gpt-4o", Label: "OpenAI"}}, nil
}
func (fakeProviders) DiscoverModelsFor(context.Context, string) (any, error) { return nil, nil }
func (fakeProviders) SetDefaultModel(context.Context, domain.Purpose, string) (any, error) {
	return nil, nil
}
func (fakeProviders) UpdateSettings(context.Context, string, map[string]string) (domain.Settings, error) {
	return nil, nil
}

func TestProviderNodesRegisteredWhenServiceGiven(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := workflow.New(workflow.Options{Paths: paths.Paths{Data: t.TempDir()}, Logger: logger, Providers: fakeProviders{}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range svc.Specs(context.Background()) {
		if strings.HasPrefix(s.Type, "tool.provider_") {
			n++
		}
	}
	if n != 8 {
		t.Fatalf("provider nodes = %d, want 8", n)
	}
	for _, src := range []string{"provider.saved", "provider.credentials", "provider.owners", "provider.models"} {
		opts, err := svc.Registry().Options(context.Background(), src, nil)
		if err != nil || len(opts) != 1 {
			t.Errorf("%s: %v %v", src, opts, err)
		}
	}
}

func TestNoProviderNodesWithoutService(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := workflow.New(workflow.Options{Paths: paths.Paths{Data: t.TempDir()}, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range svc.Specs(context.Background()) {
		if strings.HasPrefix(s.Type, "tool.provider_") {
			t.Errorf("unexpected %s", s.Type)
		}
	}
	if _, err := svc.Registry().Options(context.Background(), "provider.saved", nil); err == nil {
		t.Error("provider.saved should be unknown")
	}
}
