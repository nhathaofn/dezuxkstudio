//go:build !windows

package security

import "errors"

// ErrDPAPIUnsupported indicates that DPAPI is only supported on Windows.
var ErrDPAPIUnsupported = errors.New("dpapi: unsupported platform (Windows only)")

func EncryptDPAPI(data []byte) ([]byte, error) {
	return nil, ErrDPAPIUnsupported
}

func DecryptDPAPI(cipher []byte) ([]byte, error) {
	return nil, ErrDPAPIUnsupported
}
