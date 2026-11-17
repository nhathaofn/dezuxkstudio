package preferences_test

import (
	"path/filepath"
	"testing"

	"app/internal/apperr"
	"app/internal/domain/preferences"
)

func TestDefaults(t *testing.T) {
	d := preferences.Defaults()
	if d.SchemaVersion != preferences.SchemaVersion {
		t.Fatalf("expected schema version %d, got %d", preferences.SchemaVersion, d.SchemaVersion)
	}
	if d.SchemaVersion != 2 {
		t.Fatalf("expected schema version 2, got %d", d.SchemaVersion)
	}
	if d.Theme != preferences.ThemeLight {
		t.Fatalf("expected theme light, got %s", d.Theme)
	}
	if d.Language != preferences.LanguageEnglish {
		t.Fatalf("expected language en, got %s", d.Language)
	}
	if d.WorkspaceRoot != "" {
		t.Fatalf("expected empty workspace root, got %q", d.WorkspaceRoot)
	}
	if d.OnboardingCompleted {
		t.Fatalf("expected onboarding_completed to be false")
	}
	if d.AgentSkipTrustedApproval {
		t.Fatalf("expected agent_skip_trusted_approval to be false")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
}

func TestValidate(t *testing.T) {
	p := preferences.Defaults()
	p.Theme = "invalid-theme"
	if err := p.Validate(); err == nil {
		t.Fatalf("expected error for invalid theme")
	}

	p = preferences.Defaults()
	p.Language = "fr"
	if err := p.Validate(); err == nil {
		t.Fatalf("expected error for unsupported language")
	}

	// Valid WorkspaceRoot: empty string or absolute path
	p = preferences.Defaults()
	p.WorkspaceRoot = ""
	if err := p.Validate(); err != nil {
		t.Fatalf("empty workspace root must be valid: %v", err)
	}

	p = preferences.Defaults()
	p.WorkspaceRoot = filepath.Join(t.TempDir(), "studio")
	if err := p.Validate(); err != nil {
		t.Fatalf("absolute workspace root must be valid: %v", err)
	}

	// Invalid WorkspaceRoot: NUL byte
	p = preferences.Defaults()
	p.WorkspaceRoot = "C:\\path\x00\\bad"
	if err := p.Validate(); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for NUL byte in workspace root, got: %v", err)
	}

	// Invalid WorkspaceRoot: relative path
	p = preferences.Defaults()
	p.WorkspaceRoot = "relative/path"
	if err := p.Validate(); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for relative workspace root, got: %v", err)
	}

	// Invalid WorkspaceRoot: whitespace only
	p = preferences.Defaults()
	p.WorkspaceRoot = "   "
	if err := p.Validate(); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error for whitespace-only workspace root, got: %v", err)
	}
}

func TestApply(t *testing.T) {
	p := preferences.Defaults()
	darkTheme := preferences.ThemeDark
	viLang := preferences.LanguageVietnamese
	wsRoot := filepath.Join(t.TempDir(), "custom_ws")
	onboarded := true
	skipApproval := true

	patch := preferences.Patch{
		Theme:                    &darkTheme,
		Language:                 &viLang,
		WorkspaceRoot:            &wsRoot,
		OnboardingCompleted:      &onboarded,
		AgentSkipTrustedApproval: &skipApproval,
	}

	updated, err := p.Apply(patch)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if updated.Theme != darkTheme {
		t.Fatalf("expected theme %s, got %s", darkTheme, updated.Theme)
	}
	if updated.Language != viLang {
		t.Fatalf("expected language %s, got %s", viLang, updated.Language)
	}
	if updated.WorkspaceRoot != wsRoot {
		t.Fatalf("expected workspace root %s, got %s", wsRoot, updated.WorkspaceRoot)
	}
	if !updated.OnboardingCompleted {
		t.Fatalf("expected onboarding_completed to be true")
	}
	if !updated.AgentSkipTrustedApproval {
		t.Fatalf("expected agent_skip_trusted_approval to be true")
	}

	// Invalid patch
	relPath := "some/relative/path"
	invalidPatch := preferences.Patch{
		WorkspaceRoot: &relPath,
	}
	if _, err := p.Apply(invalidPatch); err == nil || !apperr.IsCode(err, apperr.CodeValidation) {
		t.Fatalf("expected validation error applying relative path: %v", err)
	}
}

func TestMigrate(t *testing.T) {
	// Case 1: Newer schema version is rejected
	futurePref := preferences.Preferences{
		SchemaVersion: 999,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageEnglish,
	}
	if _, err := preferences.Migrate(futurePref); err == nil || !apperr.IsCode(err, apperr.CodeConflict) {
		t.Fatalf("expected conflict error for future schema version, got: %v", err)
	}

	// Case 2: Schema v1 document migration to v2
	v1Pref := preferences.Preferences{
		SchemaVersion: 1,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageVietnamese,
	}
	migratedV1, err := preferences.Migrate(v1Pref)
	if err != nil {
		t.Fatalf("v1 migrate failed: %v", err)
	}
	if migratedV1.SchemaVersion != 2 {
		t.Fatalf("expected schema version 2, got %d", migratedV1.SchemaVersion)
	}
	if migratedV1.Theme != preferences.ThemeDark || migratedV1.Language != preferences.LanguageVietnamese {
		t.Fatalf("expected valid theme/lang preserved: %+v", migratedV1)
	}
	if migratedV1.WorkspaceRoot != "" {
		t.Fatalf("expected empty workspace root, got %q", migratedV1.WorkspaceRoot)
	}
	if migratedV1.OnboardingCompleted {
		t.Fatalf("expected onboarding_completed false")
	}
	if migratedV1.AgentSkipTrustedApproval {
		t.Fatalf("expected agent_skip_trusted_approval false")
	}

	// Case 3: Legacy document (SchemaVersion < 1)
	legacyPref := preferences.Preferences{
		SchemaVersion: 0,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageVietnamese,
	}
	migrated, err := preferences.Migrate(legacyPref)
	if err != nil {
		t.Fatalf("legacy migrate failed: %v", err)
	}
	if migrated.SchemaVersion != preferences.SchemaVersion {
		t.Fatalf("expected schema version %d, got %d", preferences.SchemaVersion, migrated.SchemaVersion)
	}
	if migrated.Theme != preferences.ThemeDark || migrated.Language != preferences.LanguageVietnamese {
		t.Fatalf("expected valid theme/lang preserved: %+v", migrated)
	}
	if migrated.WorkspaceRoot != "" || migrated.OnboardingCompleted || migrated.AgentSkipTrustedApproval {
		t.Fatalf("expected default values for new fields: %+v", migrated)
	}

	// Case 4: Current schema (v2) is preserved
	currentPref := preferences.Preferences{
		SchemaVersion:            2,
		Theme:                    preferences.ThemeDark,
		Language:                 preferences.LanguageEnglish,
		WorkspaceRoot:            filepath.Join(t.TempDir(), "current"),
		OnboardingCompleted:      true,
		AgentSkipTrustedApproval: true,
	}
	migratedCurrent, err := preferences.Migrate(currentPref)
	if err != nil {
		t.Fatalf("current migrate failed: %v", err)
	}
	if migratedCurrent != currentPref {
		t.Fatalf("expected current pref to remain unchanged: got %+v, want %+v", migratedCurrent, currentPref)
	}
}
