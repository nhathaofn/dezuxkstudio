// Package preferences is the domain model for user preferences.
package preferences

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"app/internal/apperr"
)

// SchemaVersion is the current persisted schema version. Bump it together with a
// migration step in Migrate when the shape of Preferences changes.
const SchemaVersion = 2

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
	SchemaVersion            int      `json:"schema_version"`
	Theme                    Theme    `json:"theme"`
	Language                 Language `json:"language"`
	WorkspaceRoot            string   `json:"workspace_root"`
	OnboardingCompleted      bool     `json:"onboarding_completed"`
	AgentSkipTrustedApproval bool     `json:"agent_skip_trusted_approval"`
}

// Patch updates a subset of fields; nil means "unchanged".
type Patch struct {
	Theme                    *Theme
	Language                 *Language
	WorkspaceRoot            *string
	OnboardingCompleted      *bool
	AgentSkipTrustedApproval *bool
}

// Defaults returns the preferences of a fresh installation.
func Defaults() Preferences {
	return Preferences{
		SchemaVersion:            SchemaVersion,
		Theme:                    ThemeLight,
		Language:                 LanguageEnglish,
		WorkspaceRoot:            "",
		OnboardingCompleted:      false,
		AgentSkipTrustedApproval: false,
	}
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
	if strings.ContainsRune(p.WorkspaceRoot, 0) {
		return apperr.Validation("workspace_root must not contain NUL bytes").
			WithDetails(map[string]any{"field": "workspace_root"})
	}
	if trimmed := strings.TrimSpace(p.WorkspaceRoot); trimmed != "" {
		if !filepath.IsAbs(trimmed) {
			return apperr.Validation(fmt.Sprintf("workspace_root must be an absolute path, got %q", p.WorkspaceRoot)).
				WithDetails(map[string]any{"field": "workspace_root"})
		}
	} else if p.WorkspaceRoot != "" {
		return apperr.Validation(fmt.Sprintf("workspace_root must be an absolute path, got %q", p.WorkspaceRoot)).
			WithDetails(map[string]any{"field": "workspace_root"})
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
	if patch.WorkspaceRoot != nil {
		p.WorkspaceRoot = *patch.WorkspaceRoot
	}
	if patch.OnboardingCompleted != nil {
		p.OnboardingCompleted = *patch.OnboardingCompleted
	}
	if patch.AgentSkipTrustedApproval != nil {
		p.AgentSkipTrustedApproval = *patch.AgentSkipTrustedApproval
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
	case p.SchemaVersion == 1:
		// Migrate v1 to v2: keep theme and language, initialize new fields with defaults.
		d := Defaults()
		if slices.Contains(Themes(), p.Theme) {
			d.Theme = p.Theme
		}
		if slices.Contains(Languages(), p.Language) {
			d.Language = p.Language
		}
		d.SchemaVersion = SchemaVersion
		d.WorkspaceRoot = ""
		d.OnboardingCompleted = false
		d.AgentSkipTrustedApproval = false
		return d, d.Validate()
	case p.SchemaVersion < 1:
		// Unknown/legacy document: keep valid fields, reset the rest.
		d := Defaults()
		if slices.Contains(Themes(), p.Theme) {
			d.Theme = p.Theme
		}
		if slices.Contains(Languages(), p.Language) {
			d.Language = p.Language
		}
		return d, d.Validate()
	}
	return p, p.Validate()
}

// Repository persists the preference document.
type Repository interface {
	// Load returns the stored document; found is false when nothing was saved yet.
	Load(ctx context.Context) (p Preferences, found bool, err error)
	Save(ctx context.Context, p Preferences) error
}
