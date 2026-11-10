package desktop

import (
	"testing"

	"app/internal/config"
)

func TestResolveDesktopSize(t *testing.T) {
	base := config.DesktopConfig{
		Width:     0,
		Height:    0,
		MinWidth:  720,
		MinHeight: 480,
	}

	tests := []struct {
		name                             string
		cfg                              config.DesktopConfig
		screenW, screenH                 int
		screenOK                         bool
		wantW, wantH, wantMinW, wantMinH int
	}{
		{
			name:     "1080p auto 80 percent",
			cfg:      base,
			screenW:  1920,
			screenH:  1080,
			screenOK: true,
			wantW:    1536,
			wantH:    864,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name:     "1440p auto 80 percent",
			cfg:      base,
			screenW:  2560,
			screenH:  1440,
			screenOK: true,
			wantW:    2048,
			wantH:    1152,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name:     "1366x768 auto 80 percent",
			cfg:      base,
			screenW:  1366,
			screenH:  768,
			screenOK: true,
			wantW:    1093,
			wantH:    614,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name: "partial override width explicit height auto",
			cfg: config.DesktopConfig{
				Width:     1200,
				Height:    0,
				MinWidth:  720,
				MinHeight: 480,
			},
			screenW:  1920,
			screenH:  1080,
			screenOK: true,
			wantW:    1200,
			wantH:    864,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name: "full explicit override skips screen scaling",
			cfg: config.DesktopConfig{
				Width:     1280,
				Height:    800,
				MinWidth:  720,
				MinHeight: 480,
			},
			screenW:  1920,
			screenH:  1080,
			screenOK: true,
			wantW:    1280,
			wantH:    800,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name:     "fallback when primary screen unavailable",
			cfg:      base,
			screenW:  0,
			screenH:  0,
			screenOK: false,
			wantW:    1024,
			wantH:    680,
			wantMinW: 720,
			wantMinH: 480,
		},
		{
			name:     "tiny screen clamps mins",
			cfg:      base,
			screenW:  640,
			screenH:  400,
			screenOK: true,
			wantW:    640,
			wantH:    400,
			wantMinW: 640,
			wantMinH: 400,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			getPrimary := func() (int, int, bool) {
				called = true
				return tc.screenW, tc.screenH, tc.screenOK
			}

			gotW, gotH, gotMinW, gotMinH := resolveDesktopSize(tc.cfg, getPrimary)
			if gotW != tc.wantW || gotH != tc.wantH || gotMinW != tc.wantMinW || gotMinH != tc.wantMinH {
				t.Fatalf("resolveDesktopSize() = (%d, %d, min %d, %d), want (%d, %d, min %d, %d)",
					gotW, gotH, gotMinW, gotMinH, tc.wantW, tc.wantH, tc.wantMinW, tc.wantMinH)
			}
			if tc.cfg.Width > 0 && tc.cfg.Height > 0 && called {
				t.Fatalf("expected getPrimary not to be called when both Width and Height are explicit")
			}
		})
	}
}
