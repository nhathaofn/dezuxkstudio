package modules_test

import (
	"context"
	"testing"

	"app/internal/contracts"
	domainprefs "app/internal/domain/preferences"
	"app/internal/realtime"
	"app/internal/realtime/modules"
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

func TestPreferencesModule_GetAndUpdate(t *testing.T) {
	repo := &memRepo{}

	var updated []contracts.Preferences
	prefSvc, err := preferences.New(repo, func(p domainprefs.Preferences) {
		updated = append(updated, contracts.NewPreferences(p))
	})
	if err != nil {
		t.Fatal(err)
	}

	mod := modules.Preferences{Service: prefSvc}
	actionNames := contracts.ActionNames()
	reg := realtime.NewRegistry(actionNames)
	mod.Register(reg)

	// 1. Initial preferences:get returns default theme and language
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
	if prefsRes.Theme != domainprefs.ThemeLight || prefsRes.Language != domainprefs.LanguageEnglish {
		t.Fatalf("unexpected defaults: %+v", prefsRes)
	}

	// 2. preferences:update updates theme and language and notifies listener
	hUpdate, ok := reg.Lookup(contracts.ActionPreferencesUpdate)
	if !ok || hUpdate == nil {
		t.Fatal("handler for ActionPreferencesUpdate not found")
	}
	res, err = hUpdate(context.Background(), &realtime.Request{}, []byte(`{"theme":"dark","language":"vi"}`))
	if err != nil {
		t.Fatalf("ActionPreferencesUpdate failed: %v", err)
	}
	updateRes, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if updateRes.Theme != domainprefs.ThemeDark || updateRes.Language != domainprefs.LanguageVietnamese {
		t.Fatalf("unexpected updated preferences: %+v", updateRes)
	}
	if len(updated) != 1 || updated[0] != updateRes {
		t.Fatalf("expected 1 onChange notification matching %+v, got %+v", updateRes, updated)
	}
}
