package security

import "errors"

// ErrDPAPIUnsupported indicates that Windows DPAPI is not available on this OS.
var ErrDPAPIUnsupported = errors.New("dpapi: unsupported platform (Windows only)")
