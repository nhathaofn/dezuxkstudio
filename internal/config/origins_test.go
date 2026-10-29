package config

import (
	"testing"

	"app/internal/security"
)

// Regression: the Windows webview in `wails dev` sends "http://wails.localhost:34115".
// Rejecting it silently broke every realtime feature of the desktop app.
func TestWebviewOriginsAreAllowed(t *testing.T) {
	prod := DesktopOrigins()
	for _, origin := range []string{"wails://wails", "http://wails.localhost", "https://wails.localhost"} {
		if !security.OriginAllowed(origin, prod) {
			t.Errorf("production webview origin %q rejected", origin)
		}
	}

	dev := append(DesktopOrigins(), DevOrigins()...)
	for _, origin := range []string{"http://wails.localhost:34115", "http://localhost:34115", "http://localhost:5173"} {
		if !security.OriginAllowed(origin, dev) {
			t.Errorf("dev webview origin %q rejected", origin)
		}
	}

	if security.OriginAllowed("https://evil.example", dev) {
		t.Error("foreign origin accepted")
	}
}
