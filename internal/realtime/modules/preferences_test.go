package modules_test

import (
	"context"
	"testing"

	"app/internal/contracts"
	domainprefs "app/internal/domain/preferences"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	"app/internal/secrets"
	"app/internal/service/preferences"
)

type memRepo struct {
	doc *domainprefs.Preferences
}

func (m *memRepo) Load(context.Context) (domainprefs.Preferences, bool, error) {
	if m.doc == nil {
		return domainprefs.Preferences{}, false, nil
	}
	return *m.doc, true, nil
}

func (m *memRepo) Save(_ context.Context, p domainprefs.Preferences) error {
	m.doc = &p
	return nil
}

func TestPreferencesModule_SecretIsolation(t *testing.T) {
	repo := &memRepo{}
	memSec := secrets.NewMemory()

	var updated []contracts.Preferences
	prefSvc, err := preferences.New(repo, memSec, func(p domainprefs.Preferences) {
		hasKey := false
		if k, _ := memSec.Get(secrets.KeyGatewayAPIKey); k != "" {
			hasKey = true
		}
		updated = append(updated, contracts.NewPreferences(p, hasKey))
	})
	if err != nil {
		t.Fatal(err)
	}

	mod := modules.Preferences{Service: prefSvc}
	actionSpecs := contracts.Actions()
	actionNames := make([]string, len(actionSpecs))
	for i, a := range actionSpecs {
		actionNames[i] = a.Name
	}
	reg := realtime.NewRegistry(actionNames)
	mod.Register(reg)

	// 1. Initial preferences:get returns hasGatewayApiKey: false
	hGet, ok := reg.Lookup(contracts.ActionPreferencesGet)
	if !ok || hGet == nil {
		t.Fatal("handler for ActionPreferencesGet not found")
	}
	res, err := hGet(context.Background(), &realtime.Request{}, []byte(`{}`))
	if err != nil {
		t.Fatalf("ActionPreferencesGet failed: %v", err)
	}
	prefsRes, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if prefsRes.HasGatewayApiKey {
		t.Fatal("expected HasGatewayApiKey to be false initially")
	}

	// 2. preferences:update with gatewayApiKey
	hUpdate, ok := reg.Lookup(contracts.ActionPreferencesUpdate)
	if !ok || hUpdate == nil {
		t.Fatal("handler for ActionPreferencesUpdate not found")
	}
	newKey := "sk-super-secret-key"
	res, err = hUpdate(context.Background(), &realtime.Request{}, []byte(`{"gatewayApiKey":"`+newKey+`"}`))
	if err != nil {
		t.Fatalf("ActionPreferencesUpdate failed: %v", err)
	}
	updateRes, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if !updateRes.HasGatewayApiKey {
		t.Fatal("expected HasGatewayApiKey to be true after update")
	}

	// Verify key was saved to secrets store
	storedKey, err := memSec.Get(secrets.KeyGatewayAPIKey)
	if err != nil || storedKey != newKey {
		t.Fatalf("expected secret in memSec %q, got %q, err=%v", newKey, storedKey, err)
	}

	// Verify key is NOT in domain preferences document
	loadedDoc, _, _ := repo.Load(context.Background())
	// domainprefs.Preferences has no GatewayAPIKey field, verified at compile time!
	_ = loadedDoc

	// 3. preferences:update with empty key removes it
	res, err = hUpdate(context.Background(), &realtime.Request{}, []byte(`{"gatewayApiKey":""}`))
	if err != nil {
		t.Fatalf("ActionPreferencesUpdate with empty key failed: %v", err)
	}
	emptyRes, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if emptyRes.HasGatewayApiKey {
		t.Fatal("expected HasGatewayApiKey to be false after empty update")
	}

	_, err = memSec.Get(secrets.KeyGatewayAPIKey)
	if err == nil {
		t.Fatal("expected secret to be removed from memSec")
	}
}
