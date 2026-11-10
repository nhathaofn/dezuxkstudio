package config

import "testing"

func TestAutoWindowSizeDefaults(t *testing.T) {
	cfg := Default(ModeDesktop)
	if cfg.Desktop.Width != 0 || cfg.Desktop.Height != 0 {
		t.Fatalf("expected Desktop default Width/Height to be 0 (auto), got %dx%d", cfg.Desktop.Width, cfg.Desktop.Height)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default(ModeDesktop).Validate() failed: %v", err)
	}

	// Partial auto (one dimension explicit, the other auto) must also validate.
	partial := Default(ModeDesktop)
	partial.Desktop.Width = 1280
	if err := partial.Validate(); err != nil {
		t.Fatalf("partial auto desktop size failed Validate(): %v", err)
	}
}
