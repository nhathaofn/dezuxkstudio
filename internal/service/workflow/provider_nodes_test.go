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
	if n != 5 {
		t.Fatalf("provider nodes = %d, want 5", n)
	}
	for _, src := range []string{"provider.saved", "provider.credentials"} {
		opts, err := svc.Registry().Options(context.Background(), src, nil)
		if err != nil || len(opts) != 1 || opts[0].Value != "c1" {
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
