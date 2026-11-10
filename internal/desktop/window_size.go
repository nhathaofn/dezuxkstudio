package desktop

import (
	"app/internal/config"
	"app/internal/platform"
)

func defaultWindowSize(d config.DesktopConfig) (width, height, minW, minH int) {
	return resolveDesktopSize(d, platform.PrimaryScreenSize)
}

// resolveDesktopSize computes the initial and minimum window dimensions.
// When desktop.width or desktop.height is 0 (auto), it resolves the dimension
// to 80% of the primary monitor's logical size (falling back to 1024x680 if
// screen dimensions are unavailable) while respecting configured minimums.
func resolveDesktopSize(d config.DesktopConfig, getPrimary func() (int, int, bool)) (width, height, minW, minH int) {
	width = d.Width
	height = d.Height
	minW = d.MinWidth
	minH = d.MinHeight

	if width == 0 || height == 0 {
		var sw, sh int
		if getPrimary != nil {
			if w, h, ok := getPrimary(); ok {
				sw, sh = w, h
			}
		}
		autoW, autoH := platform.ScaleWindowSize(sw, sh, d.MinWidth, d.MinHeight, platform.DefaultWindowRatio)
		if width == 0 {
			width = autoW
		}
		if height == 0 {
			height = autoH
		}
	}

	// On ultra-small displays (e.g. 640x400), prevent Wails from enforcing a
	// minimum size larger than the resolved screen-clamped window size.
	if minW > width && width > 0 {
		minW = width
	}
	if minH > height && height > 0 {
		minH = height
	}
	return width, height, minW, minH
}
