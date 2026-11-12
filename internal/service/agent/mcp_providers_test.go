package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	domainagent "app/internal/domain/agent"
	domainprovider "app/internal/domain/provider"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeProviders struct {
	setupReq domainprovider.SetupRequest
	deleted  []string
}

func (f *fakeProviders) AgentOverview(context.Context) (any, error) {
	return map[string]any{"builtin": []string{}}, nil
}
func (f *fakeProviders) PlanSetup(r domainprovider.SetupRequest) (domainprovider.SetupPlan, error) {
	return domainprovider.PlanSetup(r)
}
func (f *fakeProviders) AgentSetup(_ context.Context, r domainprovider.SetupRequest) (any, error) {
	f.setupReq = r
	return map[string]any{"saved": true}, nil
}
func (f *fakeProviders) AgentTest(context.Context, string) (any, error) {
	return map[string]any{"ok": true}, nil
}
func (f *fakeProviders) ActivateCredential(context.Context, string) error { return nil }
func (f *fakeProviders) AgentDelete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeProviders) Describe(context.Context, string) (string, error) { return "OpenAI / Work", nil }

func newProvidersFixture(t *testing.T) (*Service, *fakeRepo, *fakeProviders, *mcp.ClientSession) {
	t.Helper()
	base := t.TempDir()
	repo := newFakeRepo()
	fp := &fakeProviders{}
	svc, err := New(Options{
		Repo:         repo,
		DataDir:      filepath.Join(base, "data"),
		WorkspaceDir: filepath.Join(base, "data", "agent-workspace"),
		MediaDir:     filepath.Join(base, "data", "media"),
		ProfilesDir:  filepath.Join(base, "data", "agent-profiles"),
		Logger:       slog.New(slog.DiscardHandler),
		Providers:    fp,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := svc.buildMCPServer().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return svc, repo, fp, cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var payload map[string]any
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(tc.Text), &payload)
			break
		}
	}
	return payload, res.IsError
}

func TestProvidersSetupRedactsChatSecrets(t *testing.T) {
	svc, repo, fp, cs := newProvidersFixture(t)
	ctx := context.Background()
	const apiKey = "sk-live-ABCDEFGH12345678"
	const accessKey = "AK-ACCESS-12345678"
	const shortSecret = "abc"
	for i, text := range []string{"here is my key " + apiKey, "access " + accessKey + " short " + shortSecret, "unrelated"} {
		if err := repo.AppendChatMessage(ctx, domainagent.ChatMessage{ID: string(rune('a' + i)), Role: "user", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	out, isErr := callTool(t, cs, "providers_setup", map[string]any{
		"provider_id": "openai", "api_key": apiKey, "secrets": map[string]any{"access_key": accessKey, "tiny": shortSecret},
	})
	if isErr || out["saved"] != true {
		t.Fatalf("setup = %v err=%v", out, isErr)
	}
	if fp.setupReq.APIKey != apiKey || fp.setupReq.Secrets["access_key"] != accessKey {
		t.Fatalf("manager did not receive the raw request: %+v", fp.setupReq)
	}
	msgs, _ := repo.ListChatMessages(ctx, 10)
	if msgs[0].Text != "here is my key [secret saved]" {
		t.Fatalf("api key not redacted: %q", msgs[0].Text)
	}
	if !strings.Contains(msgs[1].Text, "[secret saved]") || strings.Contains(msgs[1].Text, accessKey) {
		t.Fatalf("secret map value not redacted: %q", msgs[1].Text)
	}
	if !strings.Contains(msgs[1].Text, shortSecret) {
		t.Fatalf("values under 8 chars must not be redacted: %q", msgs[1].Text)
	}
	if msgs[2].Text != "unrelated" {
		t.Fatalf("unrelated message changed: %q", msgs[2].Text)
	}
	_ = svc
}

func TestProvidersDeleteNeedsApproval(t *testing.T) {
	svc, _, fp, cs := newProvidersFixture(t)
	out, isErr := callTool(t, cs, "providers_delete", map[string]any{"id": " cred-1 "})
	if isErr || out["status"] != "needs_approval" {
		t.Fatalf("delete = %v err=%v", out, isErr)
	}
	if len(fp.deleted) != 0 {
		t.Fatal("delete executed before approval")
	}
	pending := svc.approvals.list()
	if len(pending) != 1 || pending[0].Tool != "providers_delete" {
		t.Fatalf("pending = %+v", pending)
	}
	if !strings.Contains(pending[0].Summary, "OpenAI / Work") || strings.Contains(strings.ToLower(pending[0].Summary+pending[0].Title), "sk-") {
		t.Fatalf("summary = %q title = %q", pending[0].Summary, pending[0].Title)
	}
	p, ok := svc.approvals.consume(pending[0].ID)
	if !ok {
		t.Fatal("approval not consumable")
	}
	if _, err := p.exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fp.deleted) != 1 || fp.deleted[0] != "cred-1" {
		t.Fatalf("deleted = %v", fp.deleted)
	}
}

func TestProvidersListTestActivate(t *testing.T) {
	_, _, _, cs := newProvidersFixture(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
		key  string
	}{
		{"providers_list", map[string]any{}, "builtin"},
		{"providers_test", map[string]any{"id": "x"}, "ok"},
		{"providers_activate", map[string]any{"id": "x"}, "ok"},
	} {
		out, isErr := callTool(t, cs, tc.tool, tc.args)
		if isErr || out[tc.key] == nil {
			t.Errorf("%s = %v err=%v", tc.tool, out, isErr)
		}
	}
}

func TestDezuxkMCPToolNamesCoversRegisteredTools(t *testing.T) {
	svc, _, _, _ := newProvidersFixture(t)
	docs := svc.registeredToolDocs()
	if len(docs) == 0 {
		t.Fatal("no tools registered")
	}
	registered := map[string]bool{}
	for _, d := range docs {
		registered[d.Name] = true
		if !dezuxkMCPToolNames[d.Name] {
			t.Errorf("tool %s registered but missing from dezuxkMCPToolNames", d.Name)
		}
	}
	for n := range dezuxkMCPToolNames {
		if !registered[n] {
			t.Errorf("dezuxkMCPToolNames lists %s but it is not registered", n)
		}
	}
}
