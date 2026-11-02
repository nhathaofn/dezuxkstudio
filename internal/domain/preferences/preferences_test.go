package preferences_test

import (
	"testing"

	"app/internal/domain/preferences"
)

func TestDefaults(t *testing.T) {
	d := preferences.Defaults()
	if d.SchemaVersion != preferences.SchemaVersion {
		t.Fatalf("expected schema version %d, got %d", preferences.SchemaVersion, d.SchemaVersion)
	}
	if d.Theme != preferences.ThemeLight {
		t.Fatalf("expected theme light, got %s", d.Theme)
	}
	if d.Language != preferences.LanguageEnglish {
		t.Fatalf("expected language en, got %s", d.Language)
	}
	if d.GatewayURL != "http://127.0.0.1:8080" {
		t.Fatalf("expected default gateway URL http://127.0.0.1:8080, got %s", d.GatewayURL)
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
}

func TestApply(t *testing.T) {
	p := preferences.Defaults()
	darkTheme := preferences.ThemeDark
	viLang := preferences.LanguageVietnamese
	newURL := "http://ai.example.com:9000"

	patch := preferences.Patch{
		Theme:      &darkTheme,
		Language:   &viLang,
		GatewayURL: &newURL,
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
	if updated.GatewayURL != newURL {
		t.Fatalf("expected gateway URL %s, got %s", newURL, updated.GatewayURL)
	}
}

func TestMigrate(t *testing.T) {
	// Case 1: Newer schema version is rejected
	futurePref := preferences.Preferences{
		SchemaVersion: 999,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageEnglish,
	}
	if _, err := preferences.Migrate(futurePref); err == nil {
		t.Fatalf("expected conflict error for future schema version")
	}

	// Case 2: Legacy document (SchemaVersion < 1)
	legacyPref := preferences.Preferences{
		SchemaVersion: 0,
		Theme:         preferences.ThemeDark,
		Language:      preferences.LanguageVietnamese,
		GatewayURL:    "http://custom-host:8080",
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
	if migrated.GatewayURL != "http://custom-host:8080" {
		t.Fatalf("expected custom gateway url preserved: %+v", migrated)
	}

	// Case 3: Schema version 1 without gateway URL falls back to default gateway URL
	v1Pref := preferences.Preferences{
		SchemaVersion: 1,
		Theme:         preferences.ThemeLight,
		Language:      preferences.LanguageEnglish,
		GatewayURL:    "",
	}
	migratedV1, err := preferences.Migrate(v1Pref)
	if err != nil {
		t.Fatalf("v1 migrate failed: %v", err)
	}
	if migratedV1.GatewayURL != "http://127.0.0.1:8080" {
		t.Fatalf("expected gateway URL default http://127.0.0.1:8080, got %s", migratedV1.GatewayURL)
	}
}
