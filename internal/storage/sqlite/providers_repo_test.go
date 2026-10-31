package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"app/internal/domain/providers"
)

func TestProvidersRepository(t *testing.T) {
	db := open(t, t.TempDir())
	defer db.Close()

	repo := NewProvidersRepository(db)
	ctx := context.Background()

	// 1. Test Provider CRUD
	p := &providers.Provider{
		ID:             "gemini",
		Name:           "Google Gemini",
		BaseURL:        "https://gemini.google.com",
		AuthDomain:     ".google.com",
		AllowedDomains: []string{".google.com", "gemini.google.com"},
		ProbeRules: providers.ProbeRules{
			IdentityExpressions: []string{"window.WIZ_global_data?.oPEP7c"},
			TokenExtractors:     map[string]string{"atToken": "SNlM0e"},
			DiscoveryKeywords:   []string{"GeminiService"},
		},
		SecurityRules: map[string]any{"captcha": "none"},
		Status:        providers.ProviderStatusActive,
	}

	if err := repo.SaveProvider(ctx, p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	fetchedP, err := repo.GetProvider(ctx, "gemini")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if fetchedP == nil {
		t.Fatal("expected provider, got nil")
	}
	if fetchedP.Name != "Google Gemini" || len(fetchedP.AllowedDomains) != 2 {
		t.Fatalf("unexpected provider data: %+v", fetchedP)
	}

	provList, err := repo.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(provList) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(provList))
	}

	// 2. Test Account CRUD
	paused := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	acc := &providers.Account{
		ID:          "acc-gemini-1",
		ProviderID:  "gemini",
		Label:       "Primary Account",
		Identity:    "user@gmail.com",
		Status:      providers.AccountStatusReady,
		ProfileDir:  "C:\\profiles\\acc-1",
		ProxyID:     "proxy-123",
		PausedUntil: &paused,
		Usage: []providers.QuotaUsage{
			{Feature: "chat", Remaining: 50, Total: 100},
		},
		Metadata: map[string]any{"tier": "pro"},
	}

	if err := repo.SaveAccount(ctx, acc); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	fetchedAcc, err := repo.GetAccount(ctx, "acc-gemini-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if fetchedAcc == nil || fetchedAcc.Identity != "user@gmail.com" || len(fetchedAcc.Usage) != 1 {
		t.Fatalf("unexpected account: %+v", fetchedAcc)
	}

	accs, err := repo.ListAccounts(ctx, "gemini")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accs) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accs))
	}

	// Update Account Status
	if err := repo.UpdateAccountStatus(ctx, "acc-gemini-1", providers.AccountStatusRateLimited, &paused); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}
	updatedAcc, _ := repo.GetAccount(ctx, "acc-gemini-1")
	if updatedAcc.Status != providers.AccountStatusRateLimited {
		t.Fatalf("expected rate_limited, got %s", updatedAcc.Status)
	}

	// 3. Test Transparent DPAPI AccountSecrets
	sec := &providers.AccountSecrets{
		AccountID: "acc-gemini-1",
		Cookies: []providers.Cookie{
			{
				Name:     "__Secure-1PSID",
				Value:    "secret_value_123_tiếng_việt",
				Domain:   ".google.com",
				Path:     "/",
				HTTPOnly: true,
				Zone:     providers.ZoneAuth,
			},
			{
				Name:   "cf_clearance",
				Value:  "sec_clearance_abc",
				Domain: "gemini.google.com",
				Path:   "/",
				Zone:   providers.ZoneSecurity,
			},
		},
		Tokens: map[string]string{
			"SNlM0e": "at_token_test_token_hash",
			"FdrFJe": "session_id_456",
		},
		CookieHash: "sha256_mock_hash",
	}

	if err := repo.SaveAccountSecrets(ctx, sec); err != nil {
		t.Fatalf("SaveAccountSecrets with DPAPI: %v", err)
	}

	// Verify directly in raw SQL that data is encrypted BLOB, NOT plain text
	var rawCookieBlob, rawTokenBlob []byte
	err = db.sql.QueryRowContext(ctx, "SELECT encrypted_cookies_blob, encrypted_tokens_blob FROM account_secrets WHERE account_id = 'acc-gemini-1'").
		Scan(&rawCookieBlob, &rawTokenBlob)
	if err != nil {
		t.Fatalf("query raw secrets: %v", err)
	}
	if len(rawCookieBlob) == 0 || len(rawTokenBlob) == 0 {
		t.Fatal("expected non-empty encrypted blobs")
	}
	// Raw blob should NOT contain the plain text string directly
	if string(rawCookieBlob) == "secret_value_123_tiếng_việt" {
		t.Fatal("security leak: cookie blob stored in plain text!")
	}

	// Read back through repo (transparent DPAPI decryption)
	fetchedSec, err := repo.GetAccountSecrets(ctx, "acc-gemini-1")
	if err != nil {
		t.Fatalf("GetAccountSecrets (decrypted): %v", err)
	}
	if fetchedSec == nil {
		t.Fatal("expected fetched secrets, got nil")
	}
	if len(fetchedSec.Cookies) != 2 || fetchedSec.Cookies[0].Value != "secret_value_123_tiếng_việt" {
		t.Fatalf("decrypted cookies mismatch: %+v", fetchedSec.Cookies)
	}
	if fetchedSec.Tokens["SNlM0e"] != "at_token_test_token_hash" {
		t.Fatalf("decrypted tokens mismatch: %+v", fetchedSec.Tokens)
	}
	if fetchedSec.CookieHash != "sha256_mock_hash" {
		t.Fatalf("cookie hash mismatch: %s", fetchedSec.CookieHash)
	}

	// 4. Test DynamicEndpoint
	ep := &providers.DynamicEndpoint{
		ID:                 "ep-gemini-chat",
		ProviderID:         "gemini",
		FeatureKey:         "chat_completion",
		HTTPMethod:         "POST",
		URLTemplate:        "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate",
		HeadersTemplate:    map[string]string{"X-Same-Domain": "1"},
		RPCMethodName:      "/BardFrontendService.StreamGenerate",
		ResolvedRPCID:      "ogiZ0b",
		ResolutionStrategy: "script_ast",
	}
	if err := repo.SaveEndpoint(ctx, ep); err != nil {
		t.Fatalf("SaveEndpoint: %v", err)
	}
	fetchedEP, err := repo.GetEndpoint(ctx, "gemini", "chat_completion")
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if fetchedEP == nil || fetchedEP.ResolvedRPCID != "ogiZ0b" || fetchedEP.HeadersTemplate["X-Same-Domain"] != "1" {
		t.Fatalf("unexpected dynamic endpoint: %+v", fetchedEP)
	}

	// 5. Test ProviderFeature
	feat := &providers.ProviderFeature{
		ID:               "feat-gemini-chat",
		ProviderID:       "gemini",
		FeatureKey:       "chat_completion",
		Name:             "Gemini Chat Completion",
		ZoneType:         providers.ZoneExecution,
		DAGNodeType:      "gemini.chat.generate",
		ExecutionChannel: providers.ChannelInPageFetch,
		InputsSchema:     json.RawMessage(`[{"name":"prompt","type":"text"}]`),
		OutputsSchema:    json.RawMessage(`[{"name":"reply","type":"text"}]`),
		ParamsSchema:     json.RawMessage(`[]`),
	}
	if err := repo.SaveFeature(ctx, feat); err != nil {
		t.Fatalf("SaveFeature: %v", err)
	}
	features, err := repo.ListFeatures(ctx, "gemini")
	if err != nil {
		t.Fatalf("ListFeatures: %v", err)
	}
	if len(features) != 1 || features[0].DAGNodeType != "gemini.chat.generate" {
		t.Fatalf("unexpected features: %+v", features)
	}

	// 6. Test CookiePartitionRules
	rule := &providers.CookiePartitionRule{
		ID:            "rule-1",
		ProviderID:    "gemini",
		ZoneType:      providers.ZoneAuth,
		DomainPattern: "*.google.com",
		NamePattern:   "^(__Secure.*|SID|HSID)$",
		IsRequired:    true,
	}
	if err := repo.SaveCookieRule(ctx, rule); err != nil {
		t.Fatalf("SaveCookieRule: %v", err)
	}
	rules, err := repo.ListCookieRules(ctx, "gemini")
	if err != nil {
		t.Fatalf("ListCookieRules: %v", err)
	}
	if len(rules) != 1 || !rules[0].IsRequired {
		t.Fatalf("unexpected rules: %+v", rules)
	}

	// 7. Test Cascade Delete: deleting Provider should cleanly cascade-delete Account and Secrets
	if err := repo.DeleteProvider(ctx, "gemini"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	pAfter, _ := repo.GetProvider(ctx, "gemini")
	if pAfter != nil {
		t.Fatal("expected provider to be deleted")
	}

	accAfter, _ := repo.GetAccount(ctx, "acc-gemini-1")
	if accAfter != nil {
		t.Fatal("expected account to be cascade deleted")
	}

	secAfter, _ := repo.GetAccountSecrets(ctx, "acc-gemini-1")
	if secAfter != nil {
		t.Fatal("expected account secrets to be cascade deleted")
	}
}
