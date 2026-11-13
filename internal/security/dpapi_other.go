//go:build !windows

package security

func EncryptDPAPI(data []byte) ([]byte, error) {
	return nil, ErrDPAPIUnsupported
}

func DecryptDPAPI(cipher []byte) ([]byte, error) {
	return nil, ErrDPAPIUnsupported
}
