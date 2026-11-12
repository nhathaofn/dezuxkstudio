package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
	"app/internal/secrets"
	"app/internal/storage/sqlite"
)

type env struct {
	svc     *Service
	store   *secrets.Memory
	repo    domain.Repository
	changed *int
}

func newEnv(t *testing.T, timeout time.Duration) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), sqlite.Options{
		Path:          filepath.Join(dir, "test.db"),
		BackupDir:     filepath.Join(dir, "backups"),
		BackupsToKeep: 2,
		BusyTimeout:   time.Second,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := sqlite.NewProviderRepository(db)
	store := secrets.NewMemory()
	n := 0
	var mu sync.Mutex // OnChanged runs concurrently during CheckAll
	svc, err := New(Options{Repo: repo, Secrets: store, Client: http.DefaultClient, TestTimeout: timeout, OnChanged: func() {
		mu.Lock()
		n++
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &env{svc: svc, store: store, repo: repo, changed: &n}
}

func code(err error) apperr.Code {
	if err == nil {
		return ""
	}
	return apperr.From(err).Code
}

func wantCode(t *testing.T, err error, c apperr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", c)
	}
	if got := code(err); got != c {
		t.Fatalf("code = %s, want %s (err: %v)", got, c, err)
	}
}

func detail(err error, key string) any { return apperr.From(err).Details[key] }

func (e *env) create(t *testing.T, id string, in domain.Input) domain.Credential {
	t.Helper()
	c, err := e.svc.CreateCredential(context.Background(), id, in)
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	return c
}

func apiKeyIn(key string, values map[string]string) domain.Input {
	return domain.Input{Values: values, Secrets: map[string]string{"api_key": key}}
}

func secretOf(t *testing.T, s *secrets.Memory, credID, field string) (string, error) {
	t.Helper()
	return s.Get(credSecretKey(credID, field))
}

const testKey = "sk-test-SECRET-1234567890"

func TestCreateCredentialStoresSecretAndMasksHints(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	c := e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))

	got, err := e.store.Get(secrets.Key("provider_cred_" + c.ID + "_api_key"))
	if err != nil || got != testKey {
		t.Fatalf("secret store = %q, %v", got, err)
	}
	if c.SecretHints["api_key"] != domain.MaskSecret(testKey) || c.SecretHints["api_key"] == testKey {
		t.Fatalf("hint = %q", c.SecretHints["api_key"])
	}
	if !c.Active {
		t.Fatal("first credential must be active")
	}
	if c.Name != "OpenAI" {
		t.Fatalf("default name = %q", c.Name)
	}
	if *e.changed != 1 {
		t.Fatalf("OnChanged calls = %d", *e.changed)
	}

	stored, err := e.repo.GetCredential(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", stored), testKey) {
		t.Fatal("repo credential contains plaintext secret")
	}

	snap, err := e.svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snap)
	if strings.Contains(string(raw), testKey) || strings.Contains(fmt.Sprintf("%+v", snap), testKey) {
		t.Fatal("snapshot contains plaintext secret")
	}
	if len(snap.Builtins) != len(domain.Registry()) {
		t.Fatalf("builtins = %d", len(snap.Builtins))
	}
	for _, b := range snap.Builtins {
		if b.Meta.ID == domain.IDOpenAI && (!b.Configured() || len(b.Credentials) != 1) {
			t.Fatalf("openai view = %+v", b)
		}
		if b.Meta.ID == domain.IDAnthropic && b.Configured() {
			t.Fatal("anthropic must not be configured")
		}
	}

	second := e.create(t, domain.IDOpenAI, apiKeyIn("sk-second-key-abcdefghij", nil))
	if second.Active {
		t.Fatal("second credential must not be active")
	}
}

func TestCreateCredentialValidation(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	_, err := e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{})
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{Secrets: map[string]string{"api_key": "   "}})
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{
		Secrets: map[string]string{"api_key": testKey, "bogus": "x"}})
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{
		Secrets: map[string]string{"api_key": testKey}, Values: map[string]string{"bogus": "x"}})
	wantCode(t, err, apperr.CodeValidation)

	// a non-secret field passed as a secret is rejected
	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, domain.Input{
		Secrets: map[string]string{"api_key": testKey, "base_url": "https://x.test"}})
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, apiKeyIn(testKey, map[string]string{"base_url": "ftp://x"}))
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.CreateCredential(ctx, domain.IDOpenAI, apiKeyIn(testKey, map[string]string{"base_url": "not a url"}))
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.CreateCredential(ctx, "nope", apiKeyIn(testKey, nil))
	wantCode(t, err, apperr.CodeNotFound)

	snap, _ := e.svc.List(ctx)
	for _, b := range snap.Builtins {
		if len(b.Credentials) != 0 {
			t.Fatalf("unexpected credentials for %s", b.Meta.ID)
		}
	}
	if *e.changed != 0 {
		t.Fatalf("OnChanged called %d times on failures", *e.changed)
	}
}

func TestKlingCredentialGroups(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	_, err := e.svc.CreateCredential(ctx, domain.IDKling, domain.Input{Secrets: map[string]string{"access_key": "ak-1234567890"}})
	wantCode(t, err, apperr.CodeValidation)

	c := e.create(t, domain.IDKling, domain.Input{Secrets: map[string]string{"access_key": "ak-1234567890", "secret_key": "sk-1234567890"}})
	if _, err := secretOf(t, e.store, c.ID, "access_key"); err != nil {
		t.Fatal(err)
	}
	if _, err := secretOf(t, e.store, c.ID, "secret_key"); err != nil {
		t.Fatal(err)
	}

	e.create(t, domain.IDKling, domain.Input{Secrets: map[string]string{"api_key": "kling-token-1234567"}})
}

func TestUpdateCredentialKeepsAndClearsSecrets(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	c := e.create(t, domain.IDOpenAI, apiKeyIn(testKey, map[string]string{"base_url": "https://a.test/v1"}))

	// empty secret keeps the old one; name and values change
	u, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{
		Name: "Renamed", Values: map[string]string{"base_url": "https://b.test/v1"}, Secrets: map[string]string{"api_key": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Renamed" || u.Values["base_url"] != "https://b.test/v1" {
		t.Fatalf("update = %+v", u)
	}
	if got, _ := secretOf(t, e.store, c.ID, "api_key"); got != testKey {
		t.Fatalf("secret changed: %q", got)
	}
	if u.SecretHints["api_key"] != domain.MaskSecret(testKey) {
		t.Fatalf("hint lost: %v", u.SecretHints)
	}

	// empty name keeps the old name
	u, err = e.svc.UpdateCredential(ctx, c.ID, domain.Input{})
	if err != nil || u.Name != "Renamed" {
		t.Fatalf("name = %q, %v", u.Name, err)
	}

	// new secret replaces
	newKey := "sk-new-key-0987654321"
	u, err = e.svc.UpdateCredential(ctx, c.ID, apiKeyIn(newKey, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := secretOf(t, e.store, c.ID, "api_key"); got != newKey {
		t.Fatalf("secret = %q", got)
	}
	if u.SecretHints["api_key"] != domain.MaskSecret(newKey) {
		t.Fatalf("hint = %v", u.SecretHints)
	}

	// clearing the only required secret fails validation and keeps everything
	_, err = e.svc.UpdateCredential(ctx, c.ID, domain.Input{ClearSecrets: []string{"api_key"}})
	wantCode(t, err, apperr.CodeValidation)
	if got, _ := secretOf(t, e.store, c.ID, "api_key"); got != newKey {
		t.Fatal("secret removed despite validation failure")
	}

	// unknown clear field
	_, err = e.svc.UpdateCredential(ctx, c.ID, domain.Input{ClearSecrets: []string{"bogus"}})
	wantCode(t, err, apperr.CodeValidation)

	_, err = e.svc.UpdateCredential(ctx, "missing-id", domain.Input{})
	wantCode(t, err, apperr.CodeNotFound)
}

func TestUpdateCredentialClearSecretsOnKling(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	c := e.create(t, domain.IDKling, domain.Input{Secrets: map[string]string{
		"api_key": "kling-token-1234567", "access_key": "ak-1234567890", "secret_key": "sk-1234567890"}})

	u, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{ClearSecrets: []string{"api_key"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretOf(t, e.store, c.ID, "api_key"); err != secrets.ErrNotFound {
		t.Fatalf("api_key should be removed, err = %v", err)
	}
	if _, ok := u.SecretHints["api_key"]; ok {
		t.Fatalf("hint remains: %v", u.SecretHints)
	}
	if _, err := secretOf(t, e.store, c.ID, "access_key"); err != nil {
		t.Fatal(err)
	}

	// clearing one of the remaining group fields leaves it incomplete
	_, err = e.svc.UpdateCredential(ctx, c.ID, domain.Input{ClearSecrets: []string{"secret_key"}})
	wantCode(t, err, apperr.CodeValidation)
	if _, err := secretOf(t, e.store, c.ID, "secret_key"); err != nil {
		t.Fatal("secret_key removed despite validation failure")
	}
}

func TestDeleteCredentialRemovesSecretsAndPromotes(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	a := e.create(t, domain.IDOpenAI, apiKeyIn("sk-aaaaaaaaaaaaaaaa", nil))
	b := e.create(t, domain.IDOpenAI, apiKeyIn("sk-bbbbbbbbbbbbbbbb", nil))
	if !a.Active || b.Active {
		t.Fatal("unexpected initial active state")
	}

	if err := e.svc.DeleteCredential(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := secretOf(t, e.store, a.ID, "api_key"); err != secrets.ErrNotFound {
		t.Fatalf("secret remains: %v", err)
	}
	if _, err := secretOf(t, e.store, b.ID, "api_key"); err != nil {
		t.Fatal("other secret removed")
	}
	got, err := e.repo.GetCredential(ctx, b.ID)
	if err != nil || !got.Active {
		t.Fatalf("remaining not promoted: %+v %v", got, err)
	}

	wantCode(t, e.svc.DeleteCredential(ctx, a.ID), apperr.CodeNotFound)
}

func TestActivateCredential(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDOpenAI, apiKeyIn("sk-aaaaaaaaaaaaaaaa", nil))
	b := e.create(t, domain.IDOpenAI, apiKeyIn("sk-bbbbbbbbbbbbbbbb", nil))
	before := *e.changed
	if err := e.svc.ActivateCredential(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if *e.changed != before+1 {
		t.Fatal("OnChanged not called")
	}
	r, err := e.svc.Resolve(ctx, domain.IDOpenAI)
	if err != nil || r.CredentialID != b.ID {
		t.Fatalf("resolve = %+v %v", r, err)
	}
}

func TestResolve(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()

	_, err := e.svc.Resolve(ctx, domain.IDOpenAI)
	wantCode(t, err, apperr.CodeValidation)
	if detail(err, "not_configured") != true {
		t.Fatalf("details = %v", apperr.From(err).Details)
	}
	_, err = e.svc.Resolve(ctx, "nope")
	wantCode(t, err, apperr.CodeNotFound)

	c := e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	r, err := e.svc.Resolve(ctx, domain.IDOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if r.BaseURL != "https://api.openai.com/v1" || r.Secrets["api_key"] != testKey || r.CredentialID != c.ID || r.ProviderID != domain.IDOpenAI {
		t.Fatalf("resolved = %+v", r)
	}

	// trailing slash trimmed from a custom base url
	if _, err := e.svc.UpdateCredential(ctx, c.ID, domain.Input{Values: map[string]string{"base_url": "https://gw.test/v1///"}}); err != nil {
		t.Fatal(err)
	}
	r, err = e.svc.Resolve(ctx, domain.IDOpenAI)
	if err != nil || r.BaseURL != "https://gw.test/v1" {
		t.Fatalf("base = %q, %v", r.BaseURL, err)
	}

	// secret lost from the store (e.g. in-memory store after restart)
	if err := e.store.Delete(credSecretKey(c.ID, "api_key")); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Resolve(ctx, domain.IDOpenAI)
	wantCode(t, err, apperr.CodeIntegrity)
	if detail(err, "secret_missing") != true {
		t.Fatalf("details = %v", apperr.From(err).Details)
	}
}

func TestResolveFor(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDAnthropic, apiKeyIn(testKey, nil))

	if _, err := e.svc.ResolveFor(ctx, domain.CapabilityLLM, domain.IDAnthropic); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.ResolveFor(ctx, domain.CapabilityVideo, domain.IDAnthropic)
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.ResolveFor(ctx, domain.CapabilityLLM, "nope")
	wantCode(t, err, apperr.CodeNotFound)
}

func TestCustomProviderLifecycle(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	models := []domain.CustomModel{
		{ModelID: "gpt-4o", Endpoint: domain.EndpointOpenAIChat, Enabled: true, IsDefault: true},
	}
	p, err := e.svc.CreateCustom(ctx, domain.CustomInput{
		Name: " My GW ", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://gw.test/v1/", APIKey: testKey, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "My GW" || p.BaseURL != "https://gw.test/v1" || p.KeyHint != domain.MaskSecret(testKey) {
		t.Fatalf("created = %+v", p)
	}
	if got, _ := e.store.Get(customSecretKey(p.ID)); got != testKey {
		t.Fatalf("stored key = %q", got)
	}
	if string(customSecretKey(p.ID)) != "provider_custom_"+p.ID+"_api_key" {
		t.Fatal("unexpected custom secret key")
	}

	snap, _ := e.svc.List(ctx)
	if raw, _ := json.Marshal(snap); strings.Contains(string(raw), testKey) {
		t.Fatal("snapshot leaks custom key")
	}

	rc, err := e.svc.ResolveCustom(ctx, p.ID)
	if err != nil || rc.APIKey != testKey || rc.Provider.ID != p.ID || len(rc.Provider.Models) != 1 {
		t.Fatalf("resolve custom = %+v %v", rc, err)
	}

	// update with empty key keeps it
	u, err := e.svc.UpdateCustom(ctx, p.ID, domain.CustomInput{
		Name: "Renamed", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://gw2.test", Models: models})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Renamed" || u.BaseURL != "https://gw2.test" || u.KeyHint == "" {
		t.Fatalf("updated = %+v", u)
	}
	if got, _ := e.store.Get(customSecretKey(p.ID)); got != testKey {
		t.Fatal("key lost on update")
	}

	// replace key
	if _, err := e.svc.UpdateCustom(ctx, p.ID, domain.CustomInput{
		Name: "Renamed", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://gw2.test", APIKey: "sk-another-key-99999"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.Get(customSecretKey(p.ID)); got != "sk-another-key-99999" {
		t.Fatalf("key = %q", got)
	}

	// clear key
	u, err = e.svc.UpdateCustom(ctx, p.ID, domain.CustomInput{
		Name: "Renamed", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://gw2.test", ClearAPIKey: true})
	if err != nil {
		t.Fatal(err)
	}
	if u.KeyHint != "" {
		t.Fatalf("hint = %q", u.KeyHint)
	}
	if _, err := e.store.Get(customSecretKey(p.ID)); err != secrets.ErrNotFound {
		t.Fatalf("key remains: %v", err)
	}
	rc, err = e.svc.ResolveCustom(ctx, p.ID)
	if err != nil || rc.APIKey != "" {
		t.Fatalf("resolve after clear = %+v %v", rc, err)
	}

	// invalid inputs
	_, err = e.svc.CreateCustom(ctx, domain.CustomInput{Name: "x", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "nope"})
	wantCode(t, err, apperr.CodeValidation)
	_, err = e.svc.UpdateCustom(ctx, "missing", domain.CustomInput{Name: "x", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test"})
	wantCode(t, err, apperr.CodeNotFound)

	// missing secret -> integrity
	if _, err := e.svc.UpdateCustom(ctx, p.ID, domain.CustomInput{
		Name: "Renamed", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://gw2.test", APIKey: testKey}); err != nil {
		t.Fatal(err)
	}
	_ = e.store.Delete(customSecretKey(p.ID))
	_, err = e.svc.ResolveCustom(ctx, p.ID)
	wantCode(t, err, apperr.CodeIntegrity)

	// delete
	_ = e.store.Set(customSecretKey(p.ID), testKey)
	if err := e.svc.DeleteCustom(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Get(customSecretKey(p.ID)); err != secrets.ErrNotFound {
		t.Fatal("key remains after delete")
	}
	_, err = e.svc.ResolveCustom(ctx, p.ID)
	wantCode(t, err, apperr.CodeNotFound)
	wantCode(t, e.svc.DeleteCustom(ctx, p.ID), apperr.CodeNotFound)
}

func TestStatuses(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	ctx := context.Background()
	e.create(t, domain.IDOpenAI, apiKeyIn(testKey, nil))
	if _, err := e.svc.CreateCustom(ctx, domain.CustomInput{Name: "c", DiscoveryFormat: domain.DiscoveryOpenAI, BaseURL: "https://a.test",
		Models: []domain.CustomModel{{ModelID: "m", Endpoint: domain.EndpointOpenAIChat, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.Statuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var openai, custom *Status
	for i := range st {
		switch {
		case st[i].ID == domain.IDOpenAI:
			openai = &st[i]
		case st[i].Custom:
			custom = &st[i]
		}
	}
	if openai == nil || !openai.Configured || custom == nil || !custom.Configured {
		t.Fatalf("statuses = %+v", st)
	}
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), testKey) {
		t.Fatal("status leaks secret")
	}
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error")
	}
}
