package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"app/internal/engine"
	providernodes "app/internal/tools/provider"
)

// providerWorkflow answers tool.provider_list with a state that the test can change between calls,
// like a user adding a key in Settings while the chat is open.
type providerWorkflow struct {
	fakeWorkflow
	state atomic.Value // string: JSON of the provider list result
}

func (w *providerWorkflow) RunNode(_ context.Context, nodeType string, _ json.RawMessage) (any, error) {
	if nodeType != providernodes.NodeList {
		return map[string]any{}, nil
	}
	var out any
	_ = json.Unmarshal([]byte(w.state.Load().(string)), &out)
	return out, nil
}

const onlyGoogle = `{"secret_backend":"keyring","builtin":[
 {"id":"gemini-aistudio","name":"Google AI Studio","capabilities":["llm","tts"],"configured":true,"enabled_models":46,
  "default_models":{"tts":"gemini-2.5-flash-preview-tts"},"credentials":[{"active":true,"health":{"outcome":"ok","dead":false}}]},
 {"id":"openai","name":"OpenAI","capabilities":["llm","image"],"configured":false,"enabled_models":0}],
 "custom":[],"default_models":{"tts":{"effective":"gemini-aistudio/gemini-2.5-flash-preview-tts"}}}`

const googleAndOpenAI = `{"secret_backend":"keyring","builtin":[
 {"id":"gemini-aistudio","name":"Google AI Studio","capabilities":["llm","tts"],"configured":true,"enabled_models":46,"credentials":[{"active":true}]},
 {"id":"openai","name":"OpenAI","capabilities":["llm","image"],"configured":true,"enabled_models":64,
  "credentials":[{"active":true,"health":{"outcome":"ok"}}]}],"custom":[]}`

func newSnapshotService(t *testing.T, w *providerWorkflow) *Service {
	t.Helper()
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler), Workflow: w})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func providersOf(t *testing.T, svc *Service) map[string]any {
	t.Helper()
	snap := svc.buildCapabilitiesSnapshot(context.Background())
	features := snap["dezuxkstudio_features"].(map[string]any)
	p, ok := features["providers"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities snapshot has no providers section: %v", features)
	}
	return p
}

func configuredIDs(t *testing.T, p map[string]any) []string {
	t.Helper()
	b, _ := json.Marshal(p["configured_providers"])
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range list {
		ids = append(ids, e.ID)
	}
	return ids
}

// The reported bug: the user added OpenAI, the agent kept saying only Google AI Studio was set up.
func TestCapabilitiesSnapshotShowsProvidersLive(t *testing.T) {
	w := &providerWorkflow{fakeWorkflow: fakeWorkflow{specs: []engine.Spec{}}}
	w.state.Store(onlyGoogle)
	svc := newSnapshotService(t, w)

	first := providersOf(t, svc)
	if got := strings.Join(configuredIDs(t, first), ","); got != "gemini-aistudio" {
		t.Fatalf("before: %s", got)
	}
	if list, _ := first["not_configured"].([]string); len(list) != 1 || list[0] != "openai" {
		t.Fatalf("not_configured = %v", first["not_configured"])
	}

	w.state.Store(googleAndOpenAI) // the user adds OpenAI in Settings
	second := providersOf(t, svc)
	if got := strings.Join(configuredIDs(t, second), ","); got != "gemini-aistudio,openai" {
		t.Fatalf("after adding OpenAI the snapshot must show it, got %s", got)
	}
	if second["checked_at"] == "" || second["note"] == nil {
		t.Fatal("snapshot must say it is live and when it was read")
	}
}

func TestCapabilitiesSnapshotWithoutProviderNodeStillWorks(t *testing.T) {
	svc, err := New(Options{Repo: newFakeRepo(), DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler),
		Workflow: &fakeWorkflow{specs: []engine.Spec{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	// fakeWorkflow answers every node with a map that is not a provider list: no providers section, no crash.
	snap := svc.buildCapabilitiesSnapshot(context.Background())
	if snap["dezuxkstudio_features"] == nil {
		t.Fatal("snapshot must still be built")
	}
}

func TestSnapshotCarriesNoSecrets(t *testing.T) {
	w := &providerWorkflow{fakeWorkflow: fakeWorkflow{specs: []engine.Spec{}}}
	w.state.Store(googleAndOpenAI)
	b, _ := json.Marshal(providersOf(t, newSnapshotService(t, w)))
	for _, leak := range []string{"api_key", "sk-", "aiza", "credential"} {
		if strings.Contains(strings.ToLower(string(b)), leak) {
			t.Fatalf("snapshot leaks %q: %s", leak, b)
		}
	}
}

// The policy must keep the agent from answering about changeable state from old tool results.
func TestPolicyRequiresRecheckOfChangeableState(t *testing.T) {
	policy := SystemInstructions()
	for _, want := range []string{
		"check it again every time you are asked",
		"never answer from memory",
		"Say what you checked",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("policy lost the rule %q", want)
		}
	}
}

func TestRepliesRecordWhichToolsTheyUsed(t *testing.T) {
	svc := newSnapshotService(t, &providerWorkflow{})
	svc.noteTool("m1", "tool.provider_list")
	svc.noteTool("m1", "studio_list_capabilities")
	svc.noteTool("m1", "tool.provider_list") // repeated call: listed once
	svc.noteTool("", "ignored")
	svc.noteTool("m2", "workflow_run_node")
	if got := svc.takeTools("m1"); got != "tool.provider_list, studio_list_capabilities" {
		t.Fatalf("m1 tools = %q", got)
	}
	if got := svc.takeTools("m1"); got != "" {
		t.Fatalf("tools must be forgotten once taken, got %q", got)
	}
	if got := svc.takeTools("m2"); got != "workflow_run_node" {
		t.Fatalf("m2 tools = %q", got)
	}
}

func TestPersistedReplyStoresToolsUsed(t *testing.T) {
	repo := newFakeRepo()
	svc, err := New(Options{Repo: repo, DataDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.noteTool("msg-1", "tool.provider_list")
	svc.persistAssistantMessage("claude", "msg-1", "Có hai provider.", "")
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.messages) != 1 || repo.messages[0].Tool != "tool.provider_list" {
		t.Fatalf("stored messages = %+v", repo.messages)
	}
}
