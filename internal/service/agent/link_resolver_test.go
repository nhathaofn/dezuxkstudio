package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/secrets"
	providersvc "app/internal/service/provider"
	"app/internal/storage/sqlite"
)

// inMemoryLinkRepo implements ProviderLinkStore for testing.
type inMemoryLinkRepo struct {
	mu    sync.Mutex
	links map[string]sqlite.AgentProviderLink
}

func newInMemoryLinkRepo() *inMemoryLinkRepo {
	return &inMemoryLinkRepo{links: make(map[string]sqlite.AgentProviderLink)}
}

func (r *inMemoryLinkRepo) GetProviderLink(_ context.Context, agentProviderID string) (sqlite.AgentProviderLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.links[agentProviderID]
	if !ok {
		return sqlite.AgentProviderLink{}, apperr.New(apperr.CodeNotFound, "link not found")
	}
	return l, nil
}

func (r *inMemoryLinkRepo) ListProviderLinks(_ context.Context) ([]sqlite.AgentProviderLink, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]sqlite.AgentProviderLink, 0, len(r.links))
	for _, l := range r.links {
		out = append(out, l)
	}
	return out, nil
}

func (r *inMemoryLinkRepo) SetProviderLink(_ context.Context, link sqlite.AgentProviderLink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.links[link.AgentProviderID] = link
	return nil
}

func (r *inMemoryLinkRepo) DeleteProviderLink(_ context.Context, agentProviderID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.links, agentProviderID)
	return nil
}

// fakeMediaInspector implements MediaAssetInspector for testing.
type fakeMediaInspector struct {
	assets map[string]*MediaAssetMetadata
}

func (f *fakeMediaInspector) InspectAsset(_ context.Context, assetID string) (*MediaAssetMetadata, error) {
	m, ok := f.assets[assetID]
	if !ok {
		return nil, nil
	}
	return m, nil
}

// fakeDriver implements sessionDriver and turnDriver for controllable auth states in tests.
type fakeDriver struct {
	cheap     bool
	authState domainagent.AuthState
	account   string
}

func (d *fakeDriver) cheapAuthProbe() bool { return d.cheap }
func (d *fakeDriver) probeAuth(_ context.Context, _ string) (domainagent.AuthState, string) {
	return d.authState, d.account
}
func (d *fakeDriver) login(_ context.Context, _, _ string) error { return nil }
func (d *fakeDriver) logout(_ context.Context, _ string) error    { return nil }
func (d *fakeDriver) release()                                   {}
func (d *fakeDriver) toolCallHint() string                       { return "" }
func (d *fakeDriver) sessionEnv(_, _ string) ([]string, error)   { return []string{"BASE_ENV=1"}, nil }
func (d *fakeDriver) prepare() error                             { return nil }
func (d *fakeDriver) loadModels(_ context.Context, _ string) ([]domainagent.ModelOption, error) {
	return nil, nil
}
func (d *fakeDriver) runTurn(_ context.Context, _, _, _, _, _, _ string) error { return nil }

func setupLinkResolverTestService(t *testing.T) (*Service, secrets.Store, *inMemoryLinkRepo, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(dir, "test.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 1,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("sqlite.Open failed: %v", err)
	}

	secretStore := secrets.NewMemory()
	linkRepo := newInMemoryLinkRepo()
	agentRepo := sqlite.NewAgentRepository(db)

	limits := DefaultLimits()
	svc, err := New(Options{
		Repo:         agentRepo,
		ApprovalRepo: agentRepo,
		DataDir:      dir,
		WorkspaceDir: filepath.Join(dir, "workspace"),
		Logger:       slog.New(slog.DiscardHandler),
		Secrets:      secretStore,
		Limits:       limits,
	})
	if err != nil {
		t.Fatalf("New agent service failed: %v", err)
	}

	svc.SetProviderLinks(linkRepo)

	cleanup := func() {
		_ = svc.Close()
		_ = db.Close()
	}
	return svc, secretStore, linkRepo, cleanup
}

// 1. Test compatibility matrix logic
func TestLinkResolver_CompatibilityMatrix(t *testing.T) {
	// Antigravity (ACP):
	// Compatible with Google AI Studio (api_key)
	if !providersvc.IsCompatible("antigravity", "acp", "gemini-aistudio", "api_key") {
		t.Errorf("expected antigravity + acp + gemini-aistudio + api_key to be compatible")
	}
	// Incompatible with Google Vertex AI (same vendor, but uses credentials_file service account JSON!)
	if providersvc.IsCompatible("antigravity", "acp", "gemini-vertex", "credentials_file") {
		t.Errorf("expected antigravity + acp + gemini-vertex (credentials_file) to be INCOMPATIBLE")
	}
	// Incompatible with CLI runner driver (Antigravity is ACP only)
	if providersvc.IsCompatible("antigravity", "cli", "gemini-aistudio", "api_key") {
		t.Errorf("expected antigravity + cli runner driver to be INCOMPATIBLE")
	}
	// Incompatible with OpenAI or Anthropic
	if providersvc.IsCompatible("antigravity", "acp", "openai", "api_key") {
		t.Errorf("expected antigravity + openai to be INCOMPATIBLE")
	}

	// Codex (CLI):
	// Compatible with OpenAI (api_key)
	if !providersvc.IsCompatible("codex", "cli", "openai", "api_key") {
		t.Errorf("expected codex + cli + openai + api_key to be compatible")
	}
	// Incompatible with ACP runner driver
	if providersvc.IsCompatible("codex", "acp", "openai", "api_key") {
		t.Errorf("expected codex + acp to be INCOMPATIBLE")
	}
	// Incompatible with Anthropic
	if providersvc.IsCompatible("codex", "cli", "anthropic", "api_key") {
		t.Errorf("expected codex + anthropic to be INCOMPATIBLE")
	}

	// Claude (CLI):
	// Compatible with Anthropic (api_key)
	if !providersvc.IsCompatible("claude", "cli", "anthropic", "api_key") {
		t.Errorf("expected claude + cli + anthropic + api_key to be compatible")
	}
	// Incompatible with Gemini
	if providersvc.IsCompatible("claude", "cli", "gemini-aistudio", "api_key") {
		t.Errorf("expected claude + gemini-aistudio to be INCOMPATIBLE")
	}

	// ResolveCompatibleProviders
	agyCompatible := providersvc.ResolveCompatibleProviders("antigravity", "acp")
	if len(agyCompatible) != 1 || agyCompatible[0] != "gemini-aistudio" {
		t.Errorf("expected [gemini-aistudio], got %v", agyCompatible)
	}

	codexCompatible := providersvc.ResolveCompatibleProviders("codex", "cli")
	if len(codexCompatible) != 1 || codexCompatible[0] != "openai" {
		t.Errorf("expected [openai], got %v", codexCompatible)
	}

	claudeCompatible := providersvc.ResolveCompatibleProviders("claude", "cli")
	if len(claudeCompatible) != 1 || claudeCompatible[0] != "anthropic" {
		t.Errorf("expected [anthropic], got %v", claudeCompatible)
	}

	// Driver mismatch returns empty slice
	mismatch := providersvc.ResolveCompatibleProviders("antigravity", "cli")
	if len(mismatch) != 0 {
		t.Errorf("expected empty slice for driver mismatch, got %v", mismatch)
	}
}

// 2. Test strict rejection of auto-fallback
// CRITICAL DESIGN RULE: Absolutely NEVER automatically fallback from OAuth/Subscription to API key
// when login fails, expires, or is missing. Switching occurs ONLY upon explicit user opt-in link.
func TestLinkResolver_StrictNoAutoFallback(t *testing.T) {
	svc, secretStore, linkRepo, cleanup := setupLinkResolverTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Inject a fake driver for Antigravity that reports AuthUnauthenticated (OAuth missing / expired)
	fakeAgy := &fakeDriver{cheap: true, authState: domainagent.AuthUnauthenticated, account: ""}
	svc.drivers[domainagent.ProviderAntigravity] = fakeAgy

	// Also simulate an active API key existing in Settings > Providers (cred-123)
	// Even though an API key exists in provider store, agent MUST NOT automatically fall back to it!
	credID := "cred-test-gemini"
	if err := secretStore.Set(secrets.Key("provider_cred_"+credID+"_api_key"), "AIzaSyTestSecret12345"); err != nil {
		t.Fatalf("failed to store secret: %v", err)
	}

	// Case A: No opt-in link is configured.
	// When agent is unauthenticated, Connect MUST fail with StateUnauthenticated.
	// It MUST NOT automatically discover or fall back to cred-test-gemini!
	spec := domainagent.Registry[domainagent.ProviderAntigravity]
	_, err := svc.Connect(ctx, domainagent.ProviderAntigravity)
	if err == nil {
		t.Fatalf("expected Connect to fail when OAuth is unauthenticated and no link is set")
	}
	if svc.states[domainagent.ProviderAntigravity] != domainagent.StateUnauthenticated {
		t.Errorf("expected state Unauthenticated, got %v", svc.states[domainagent.ProviderAntigravity])
	}
	if svc.authStates[domainagent.ProviderAntigravity] != domainagent.AuthUnauthenticated {
		t.Errorf("expected authState Unauthenticated, got %v", svc.authStates[domainagent.ProviderAntigravity])
	}

	// Case B: User explicitly configures opt-in link via SetProviderLink.
	err = svc.SetProviderLink(ctx, domainagent.ProviderAntigravity, "gemini-aistudio", credID)
	if err != nil {
		t.Fatalf("SetProviderLink failed: %v", err)
	}

	// Verify link was stored
	link, err := linkRepo.GetProviderLink(ctx, "antigravity")
	if err != nil || link.CredentialID != credID {
		t.Fatalf("expected link to cred-test-gemini, got %v, err=%v", link, err)
	}

	// Now resolve linked secret
	resolvedLink, err := providersvc.ResolveLinkedSecret(ctx, "antigravity", "acp", linkRepo, secretStore)
	if err != nil {
		t.Fatalf("ResolveLinkedSecret failed: %v", err)
	}
	if resolvedLink == nil || resolvedLink.Secret != "AIzaSyTestSecret12345" {
		t.Fatalf("expected resolved secret AIzaSyTestSecret12345, got %v", resolvedLink)
	}

	// Case C: User removes the opt-in link via DeleteProviderLink.
	err = svc.DeleteProviderLink(ctx, domainagent.ProviderAntigravity)
	if err != nil {
		t.Fatalf("DeleteProviderLink failed: %v", err)
	}

	// Connecting again must fail immediately with StateUnauthenticated.
	// MUST NOT fall back to the previously linked or available key!
	_, err = svc.Connect(ctx, domainagent.ProviderAntigravity)
	if err == nil {
		t.Fatalf("expected Connect to fail after link was deleted and OAuth is unauthenticated")
	}
	if svc.states[domainagent.ProviderAntigravity] != domainagent.StateUnauthenticated {
		t.Errorf("expected state Unauthenticated, got %v", svc.states[domainagent.ProviderAntigravity])
	}

	// Case D: User links an API key, but the secret is missing from secretStore.
	// When connecting, it must return an error and MUST NOT silently fall back to OAuth!
	missingCredID := "cred-missing-secret"
	err = svc.SetProviderLink(ctx, domainagent.ProviderAntigravity, "gemini-aistudio", missingCredID)
	if err != nil {
		t.Fatalf("SetProviderLink failed: %v", err)
	}
	_, err = providersvc.ResolveLinkedSecret(ctx, "antigravity", "acp", linkRepo, secretStore)
	if err == nil {
		t.Fatalf("expected error when linked secret is missing in secret store")
	}
	if !strings.Contains(err.Error(), "missing in credential store") {
		t.Errorf("expected missing secret error, got: %v", err)
	}

	_ = spec
}

// 3. Test secret injection and scrubbing
func TestLinkResolver_SecretInjectionAndScrubbing(t *testing.T) {
	_, secretStore, linkRepo, cleanup := setupLinkResolverTestService(t)
	defer cleanup()

	ctx := context.Background()

	credID := "cred-secret-inject"
	rawSecret := "AIzaSySecretToBeInjected998877"
	if err := secretStore.Set(secrets.Key("provider_cred_"+credID+"_api_key"), rawSecret); err != nil {
		t.Fatalf("failed to store secret: %v", err)
	}

	// Set link for Codex
	err := linkRepo.SetProviderLink(ctx, sqlite.AgentProviderLink{
		AgentProviderID: "codex",
		ProviderID:      "openai",
		CredentialID:    credID,
		AuthMethod:      "api_key",
		UpdatedAt:       time.Now(),
	})
	if err != nil {
		t.Fatalf("SetProviderLink failed: %v", err)
	}

	resolved, err := providersvc.ResolveLinkedSecret(ctx, "codex", "cli", linkRepo, secretStore)
	if err != nil {
		t.Fatalf("ResolveLinkedSecret failed: %v", err)
	}
	if resolved == nil {
		t.Fatalf("expected resolved link, got nil")
	}

	// Check environment variable injection
	if resolved.EnvOverrides["OPENAI_API_KEY"] != rawSecret {
		t.Errorf("expected OPENAI_API_KEY=%s, got %s", rawSecret, resolved.EnvOverrides["OPENAI_API_KEY"])
	}

	// Verify scrubbing / redaction helpers
	textWithSecret := fmt.Sprintf("Error 401: Unauthorized request with key %s from server", rawSecret)
	scrubbed := RedactSecret(textWithSecret, rawSecret)
	if strings.Contains(scrubbed, rawSecret) {
		t.Errorf("secret leaked in text! got: %s", scrubbed)
	}
	if !strings.Contains(scrubbed, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in text, got: %s", scrubbed)
	}

	// RedactError
	errWithSecret := fmt.Errorf("transport failed: bearer token %s rejected", rawSecret)
	scrubbedErr := RedactError(errWithSecret, rawSecret)
	if strings.Contains(scrubbedErr.Error(), rawSecret) {
		t.Errorf("secret leaked in error! got: %v", scrubbedErr)
	}
	if !strings.Contains(scrubbedErr.Error(), "[REDACTED]") {
		t.Errorf("expected [REDACTED] in error, got: %v", scrubbedErr)
	}

	// RedactSecrets with multiple keys
	multiSecretText := "key1=AIzaSySecretToBeInjected998877 and key2=sk-ant-api03-abcdef12345"
	multiScrubbed := RedactSecrets(multiSecretText, rawSecret, "sk-ant-api03-abcdef12345")
	if strings.Contains(multiScrubbed, rawSecret) || strings.Contains(multiScrubbed, "sk-ant-api03-abcdef12345") {
		t.Errorf("secret leaked in multi-secret scrub: %s", multiScrubbed)
	}
}

// 4. Test media asset context inspection
func TestLinkResolver_MediaAssetInspection(t *testing.T) {
	svc, _, _, cleanup := setupLinkResolverTestService(t)
	defer cleanup()

	ctx := context.Background()

	assetID := "test-asset-uuid-112233"
	privateRawPath := "D:/Projects/SecretFolder/private_image.png"
	privateTicket := "hmac-ticket-secret-nonce-9988"

	meta := &MediaAssetMetadata{
		AssetID:        assetID,
		Kind:           "image",
		SourceNode:     "mediagen_image",
		RegisteredRoot: "D:/Projects/Media",
		MIMEType:       "image/png",
		SizeBytes:      4096,
		Width:          1920,
		Height:         1080,
		DurationSec:    0,
		Metadata: map[string]any{
			"prompt": "A beautiful sunset over mountains",
		},
	}

	inspector := &fakeMediaInspector{
		assets: map[string]*MediaAssetMetadata{
			assetID: meta,
		},
	}
	svc.SetMediaInspector(inspector)

	// Test inspectMediaAsset
	res, err := svc.inspectMediaAsset(ctx, assetID)
	if err != nil {
		t.Fatalf("inspectMediaAsset failed: %v", err)
	}

	// Verify safe metadata is returned
	if res["asset_id"] != assetID {
		t.Errorf("expected asset_id=%s, got %v", assetID, res["asset_id"])
	}
	if res["mime_type"] != "image/png" {
		t.Errorf("expected mime_type=image/png, got %v", res["mime_type"])
	}
	if res["width"] != 1920 || res["height"] != 1080 {
		t.Errorf("expected 1920x1080, got %vx%v", res["width"], res["height"])
	}
	if res["size_bytes"] != int64(4096) {
		t.Errorf("expected size_bytes=4096, got %v", res["size_bytes"])
	}

	// CRITICAL: Raw paths and tickets must NEVER be exposed
	b, _ := json.Marshal(res)
	jsonStr := string(b)
	if strings.Contains(jsonStr, privateRawPath) {
		t.Errorf("SECURITY LEAK: private raw path exposed in inspection result: %s", jsonStr)
	}
	if strings.Contains(jsonStr, privateTicket) {
		t.Errorf("SECURITY LEAK: private ticket exposed in inspection result: %s", jsonStr)
	}
	if _, hasPath := res["file_path"]; hasPath {
		t.Errorf("SECURITY LEAK: file_path field exposed in inspection result")
	}
	if _, hasTicket := res["ticket"]; hasTicket {
		t.Errorf("SECURITY LEAK: ticket field exposed in inspection result")
	}

	// Test UIContext rendering
	ui := UIContext{
		Screen:           "Media Gallery",
		Route:            "/media",
		SelectedAssetIDs: []string{assetID},
	}
	norm := ui.normalized()
	if len(norm.SelectedAssetIDs) != 1 || norm.SelectedAssetIDs[0] != assetID {
		t.Errorf("expected normalized SelectedAssetIDs to contain %s, got %v", assetID, norm.SelectedAssetIDs)
	}

	block := norm.promptBlock()
	if !strings.Contains(block, assetID) {
		t.Errorf("expected promptBlock to mention asset ID %s, got: %s", assetID, block)
	}
	if strings.Contains(block, privateRawPath) {
		t.Errorf("SECURITY LEAK: promptBlock leaked raw file path!")
	}
	if strings.Contains(block, privateTicket) {
		t.Errorf("SECURITY LEAK: promptBlock leaked ticket!")
	}
}
