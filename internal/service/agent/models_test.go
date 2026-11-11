package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	domainagent "app/internal/domain/agent"
)

func TestParseCodexCatalogKeepsListedModelsInPriorityOrder(t *testing.T) {
	raw := []byte(`{"models":[
		{"slug":"b-model","display_name":"B","visibility":"list","priority":2,"default_reasoning_level":"medium",
		 "supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"}]},
		{"slug":"hidden","display_name":"Hidden","visibility":"hide","priority":0},
		{"slug":"a-model","display_name":"A","visibility":"list","priority":1,"default_reasoning_level":"low",
		 "supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]}
	]}`)
	got, err := parseCodexCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a-model" || got[1].ID != "b-model" {
		t.Fatalf("expected [a-model b-model], got %+v", got)
	}
	if !reflect.DeepEqual(got[0].Efforts, []string{"low", "high"}) || got[0].DefaultEffort != "low" {
		t.Fatalf("efforts not parsed: %+v", got[0])
	}
}

func TestParseClaudeEffortsFromHelp(t *testing.T) {
	help := "  --effort <level>                      Effort level for the current session\n                                        (low, medium, high, xhigh, max)\n"
	if got := parseClaudeEfforts(help); !reflect.DeepEqual(got, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("got %v", got)
	}
	if got := parseClaudeEfforts("no effort flag here"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestParseACPSessionModels(t *testing.T) {
	raw := []byte(`{"sessionId":"s1","models":{"availableModels":[
		{"modelId":"gemini-3.8-flash-medium","name":"Gemini 3.8 Flash (Medium)"},
		{"modelId":"gemini-4-argon-medium","name":"Gemini 4 Argon (Medium)"}],
		"currentModelId":"gemini-4-argon-medium"}}`)
	models, current := parseACPSessionModels(raw)
	if len(models) != 2 || current != "gemini-4-argon-medium" || models[0].Name != "Gemini 3.8 Flash (Medium)" {
		t.Fatalf("got %+v current=%q", models, current)
	}
}

func TestModelArgs(t *testing.T) {
	sel := domainagent.ModelSelection{Model: "gpt-x", Effort: "low"}
	if got := strings.Join(codexModelArgs(sel), " "); got != `-m gpt-x -c model_reasoning_effort="low"` {
		t.Fatalf("codex args: %s", got)
	}
	if got := strings.Join(claudeModelArgs(domainagent.ModelSelection{Model: "sonnet", Effort: "medium"}), " "); got != "--model sonnet --effort medium" {
		t.Fatalf("claude args: %s", got)
	}
	if len(codexModelArgs(domainagent.ModelSelection{})) != 0 || len(claudeModelArgs(domainagent.ModelSelection{})) != 0 {
		t.Fatal("default selection must not add flags")
	}
}

func TestAgyModelSelectionAppliedAndValidated(t *testing.T) {
	dir := t.TempDir()
	agy := filepath.Join(dir, "agy.exe")
	if err := os.WriteFile(agy, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	mock := &mockTransport{
		initRes: &InitializeResult{ProtocolVersion: 1},
		models: []domainagent.ModelOption{
			{ID: "gemini-4-argon-medium", Name: "Argon"},
			{ID: "gemini-3.8-flash-medium", Name: "Flash"},
		},
		currentModel: "gemini-4-argon-medium",
	}
	repo := newFakeRepo()
	repo.models[domainagent.ProviderAntigravity] = domainagent.ModelSelection{Model: "gemini-3.8-flash-medium"}
	svc, err := New(Options{
		Repo: repo, DataDir: dir, WorkspaceDir: filepath.Join(dir, "ws"), MediaDir: filepath.Join(dir, "media"),
		ProfilesDir: filepath.Join(dir, "profiles"), Logger: slog.New(slog.DiscardHandler),
		SpawnTransport: func(_ context.Context, _ string, _ []string, _ string, _ []string, _ func(SessionUpdate), _ func(error)) (Transport, error) {
			return mock, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	if _, err := svc.SetCustomPath(ctx, domainagent.ProviderAntigravity, agy); err != nil {
		t.Fatal(err)
	}
	if err := saveAgyAPIKey(svc.profileDirFor(domainagent.ProviderAntigravity), "AIza-test"); err != nil {
		t.Fatal(err)
	}

	info, err := svc.Connect(ctx, domainagent.ProviderAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Models) != 2 || info.CurrentModel != "gemini-3.8-flash-medium" {
		t.Fatalf("expected saved model applied on connect, got models=%d current=%q", len(info.Models), info.CurrentModel)
	}
	if !reflect.DeepEqual(mock.setModelCalls, []string{"gemini-3.8-flash-medium"}) {
		t.Fatalf("expected session/set_model with saved choice, got %v", mock.setModelCalls)
	}

	if _, err := svc.SetModel(ctx, domainagent.ProviderAntigravity, "gpt-made-up", ""); err == nil {
		t.Fatal("expected unknown model to be rejected")
	}
	info, err = svc.SetModel(ctx, domainagent.ProviderAntigravity, "gemini-4-argon-medium", "")
	if err != nil || info.SelectedModel != "gemini-4-argon-medium" || info.CurrentModel != "gemini-4-argon-medium" {
		t.Fatalf("expected live switch, got info=%+v err=%v", info, err)
	}
	if repo.models[domainagent.ProviderAntigravity].Model != "gemini-4-argon-medium" {
		t.Fatal("selection not persisted")
	}
	// Back to the provider default.
	if _, err := svc.SetModel(ctx, domainagent.ProviderAntigravity, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := mock.setModelCalls[len(mock.setModelCalls)-1]; got != "gemini-4-argon-medium" {
		t.Fatalf("expected reset to provider default model, got %q", got)
	}
}

func TestCodexSelectionValidatedAgainstCatalog(t *testing.T) {
	svc := &Service{models: newModelState(), repo: newFakeRepo(), log: slog.New(slog.DiscardHandler)}
	svc.models.catalogs[domainagent.ProviderCodex] = []domainagent.ModelOption{{ID: "gpt-a", Efforts: []string{"low", "medium"}}}
	svc.models.selection[domainagent.ProviderCodex] = domainagent.ModelSelection{Model: "gpt-a", Effort: "max"}
	if got := svc.effectiveSelection(domainagent.ProviderCodex); got.Model != "gpt-a" || got.Effort != "" {
		t.Fatalf("unsupported effort must be dropped, got %+v", got)
	}
	svc.models.selection[domainagent.ProviderCodex] = domainagent.ModelSelection{Model: "gone-model"}
	if got := svc.effectiveSelection(domainagent.ProviderCodex); got.Model != "" {
		t.Fatalf("model missing from catalog must fall back to default, got %+v", got)
	}
}
