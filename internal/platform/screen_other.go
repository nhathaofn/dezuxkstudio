//go:build !windows

package platform

// PrimaryScreenSize returns false on non-Windows hosts; desktop mode currently targets Windows.
func PrimaryScreenSize() (width, height int, ok bool) {
	return 0, 0, false
}
