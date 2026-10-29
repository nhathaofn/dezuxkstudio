// Package preferences is the domain model for user preferences.
package preferences

import (
	"context"
	"fmt"
	"slices"

	"app/internal/apperr"
)

// SchemaVersion is the current persisted schema version. Bump it together with a
// migration step in Migrate when the shape of Preferences changes.
const SchemaVersion = 1

// Theme is the UI colour scheme.
type Theme string

const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Themes lists valid themes.
func Themes() []Theme { return []Theme{ThemeSystem, ThemeLight, ThemeDark} }

// Language is a UI locale.
type Language string

const (
	LanguageEnglish    Language = "en"
	LanguageVietnamese Language = "vi"
)

// Languages lists supported locales.
func Languages() []Language { return []Language{LanguageEnglish, LanguageVietnamese} }

// Preferences is the persisted preference document.
type Preferences struct {
	SchemaVersion int      `json:"schema_version"`
	Theme         Theme    `json:"theme"`
	Language      Language `json:"language"`
}

// Patch updates a subset of fields; nil means "unchanged".
type Patch struct {
	Theme    *Theme
	Language *Language
}

// Defaults returns the preferences of a fresh installation.
func Defaults() Preferences {
	return Preferences{SchemaVersion: SchemaVersion, Theme: ThemeLight, Language: LanguageEnglish}
}

// Validate checks every field.
func (p Preferences) Validate() error {
	if !slices.Contains(Themes(), p.Theme) {
		return apperr.Validation(fmt.Sprintf("unsupported theme %q", p.Theme)).
			WithDetails(map[string]any{"field": "theme"})
	}
	if !slices.Contains(Languages(), p.Language) {
		return apperr.Validation(fmt.Sprintf("unsupported language %q", p.Language)).
			WithDetails(map[string]any{"field": "language"})
	}
	return nil
}

// Apply returns a validated copy with patch applied.
func (p Preferences) Apply(patch Patch) (Preferences, error) {
	if patch.Theme != nil {
		p.Theme = *patch.Theme
	}
	if patch.Language != nil {
		p.Language = *patch.Language
	}
	return p, p.Validate()
}

// Migrate upgrades a document loaded from storage to SchemaVersion. Documents written
// by a newer application version are rejected instead of being silently downgraded.
func Migrate(p Preferences) (Preferences, error) {
	switch {
	case p.SchemaVersion > SchemaVersion:
		return Preferences{}, apperr.New(apperr.CodeConflict,
			fmt.Sprintf("preferences were written by a newer version (schema %d)", p.SchemaVersion))
	case p.SchemaVersion < 1:
		// Unknown/legacy document: keep valid fields, reset the rest.
		d := Defaults()
		if slices.Contains(Themes(), p.Theme) {
			d.Theme = p.Theme
		}
		if slices.Contains(Languages(), p.Language) {
			d.Language = p.Language
		}
		return d, nil
	}
	// Future: add `case p.SchemaVersion == 1:` steps here when SchemaVersion becomes 2.
	return p, p.Validate()
}

// Repository persists the preference document.
type Repository interface {
	// Load returns the stored document; found is false when nothing was saved yet.
	Load(ctx context.Context) (p Preferences, found bool, err error)
	Save(ctx context.Context, p Preferences) error
}
