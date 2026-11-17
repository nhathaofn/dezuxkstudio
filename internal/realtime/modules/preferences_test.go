package modules_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"app/internal/apperr"
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

func TestPreferencesModule_DesktopMode_AllowsAllFields(t *testing.T) {
	repo := &memRepo{}
	prefSvc, err := preferences.New(repo, nil)
	if err != nil {
		t.Fatal(err)
	}

	mod := modules.Preferences{
		Service:            prefSvc,
		LockTrustedFolders: false,
	}
	reg := realtime.NewRegistry(contracts.ActionNames())
	mod.Register(reg)

	hUpdate, ok := reg.Lookup(contracts.ActionPreferencesUpdate)
	if !ok || hUpdate == nil {
		t.Fatal("handler for ActionPreferencesUpdate not found")
	}

	absDir := t.TempDir()
	patchMap := map[string]any{
		"workspace_root":              absDir,
		"onboarding_completed":        true,
		"agent_skip_trusted_approval": true,
	}
	payloadBytes, err := json.Marshal(patchMap)
	if err != nil {
		t.Fatal(err)
	}

	res, err := hUpdate(context.Background(), &realtime.Request{}, payloadBytes)
	if err != nil {
		t.Fatalf("ActionPreferencesUpdate in desktop mode failed: %v", err)
	}
	updated, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if updated.WorkspaceRoot != absDir {
		t.Errorf("WorkspaceRoot = %q, want %q", updated.WorkspaceRoot, absDir)
	}
	if !updated.OnboardingCompleted {
		t.Errorf("OnboardingCompleted = false, want true")
	}
	if !updated.AgentSkipTrustedApproval {
		t.Errorf("AgentSkipTrustedApproval = false, want true")
	}
}

func TestPreferencesModule_ServerMode_BlocksTrustedFolders(t *testing.T) {
	repo := &memRepo{}
	prefSvc, err := preferences.New(repo, nil)
	if err != nil {
		t.Fatal(err)
	}

	mod := modules.Preferences{
		Service:            prefSvc,
		LockTrustedFolders: true,
	}
	reg := realtime.NewRegistry(contracts.ActionNames())
	mod.Register(reg)

	hUpdate, ok := reg.Lookup(contracts.ActionPreferencesUpdate)
	if !ok || hUpdate == nil {
		t.Fatal("handler for ActionPreferencesUpdate not found")
	}

	const wantErr = "trusted folders and workspace settings can only be changed in the desktop app on this machine"

	assertUnavailable := func(name string, payloadObj any) {
		t.Helper()
		raw, err := json.Marshal(payloadObj)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hUpdate(context.Background(), &realtime.Request{}, raw)
		if err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeUnavailable {
			t.Fatalf("%s: expected ERR_UNAVAILABLE, got %v", name, err)
		}
		if ae.Message != wantErr {
			t.Fatalf("%s: expected message %q, got %q", name, wantErr, ae.Message)
		}
	}

	absDir := t.TempDir()
	assertUnavailable("workspace_root", map[string]any{"workspace_root": absDir})
	assertUnavailable("onboarding_completed", map[string]any{"onboarding_completed": true})
	assertUnavailable("agent_skip_trusted_approval", map[string]any{"agent_skip_trusted_approval": true})

	// Theme and language are NOT locked in server mode:
	res, err := hUpdate(context.Background(), &realtime.Request{}, []byte(`{"theme":"dark","language":"vi"}`))
	if err != nil {
		t.Fatalf("updating theme and language in server mode failed: %v", err)
	}
	updated, ok := res.(contracts.Preferences)
	if !ok {
		t.Fatalf("expected contracts.Preferences, got %T", res)
	}
	if updated.Theme != domainprefs.ThemeDark || updated.Language != domainprefs.LanguageVietnamese {
		t.Fatalf("unexpected updated theme/language: %+v", updated)
	}
}
