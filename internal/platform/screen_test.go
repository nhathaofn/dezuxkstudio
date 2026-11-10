package platform

import (
	"runtime"
	"testing"
)

func TestScaleWindowSize(t *testing.T) {
	cases := []struct {
		name             string
		screenW, screenH int
		minW, minH       int
		wantW, wantH     int
	}{
		{"1080p", 1920, 1080, 720, 480, 1536, 864},
		{"1440p", 2560, 1440, 720, 480, 2048, 1152},
		{"1366x768_laptop", 1366, 768, 720, 480, 1093, 614},
		{"720p", 1280, 720, 720, 480, 1024, 576},
		{"small_clamped_to_min", 800, 600, 720, 480, 720, 480},
		{"tiny_screen_capped_to_screen", 640, 400, 720, 480, 640, 400},
		{"unavailable_fallback", 0, 0, 720, 480, FallbackWindowWidth, FallbackWindowHeight},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := ScaleWindowSize(tc.screenW, tc.screenH, tc.minW, tc.minH, DefaultWindowRatio)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("ScaleWindowSize(%d, %d) = %dx%d, want %dx%d",
					tc.screenW, tc.screenH, w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestPrimaryScreenSizeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only primary monitor check")
	}
	w, h, ok := PrimaryScreenSize()
	if !ok {
		t.Skip("no interactive display detected in this session")
	}
	if w < 320 || h < 240 {
		t.Fatalf("unexpected primary screen size: %dx%d", w, h)
	}
	t.Logf("detected primary screen logical size: %dx%d (80%% -> %dx%d)", w, h, int(float64(w)*0.8), int(float64(h)*0.8))
}
