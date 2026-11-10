package platform

import "math"

const (
	// DefaultWindowRatio is the default fraction of the primary screen occupied by the window (80%).
	DefaultWindowRatio = 0.8
	// FallbackWindowWidth is used when the primary display dimensions cannot be queried.
	FallbackWindowWidth = 1024
	// FallbackWindowHeight is used when the primary display dimensions cannot be queried.
	FallbackWindowHeight = 680
)

// ScaleWindowSize computes a window size at ratio of the given screen dimensions,
// keeping the result at least minW x minH (capped at the screen dimensions if the screen is smaller).
func ScaleWindowSize(screenW, screenH, minW, minH int, ratio float64) (width, height int) {
	if screenW <= 0 || screenH <= 0 {
		return max(FallbackWindowWidth, minW), max(FallbackWindowHeight, minH)
	}
	if ratio <= 0 || ratio > 1 {
		ratio = DefaultWindowRatio
	}

	w := int(math.Round(float64(screenW) * ratio))
	h := int(math.Round(float64(screenH) * ratio))

	effMinW := minW
	if effMinW > screenW {
		effMinW = screenW
	}
	effMinH := minH
	if effMinH > screenH {
		effMinH = screenH
	}

	if w < effMinW {
		w = effMinW
	}
	if h < effMinH {
		h = effMinH
	}
	if w > screenW {
		w = screenW
	}
	if h > screenH {
		h = screenH
	}
	return w, h
}
