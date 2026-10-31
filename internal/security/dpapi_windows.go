//go:build windows

package security

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modcrypt32             = windows.NewLazySystemDLL("crypt32.dll")
	procCryptProtectData   = modcrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modcrypt32.NewProc("CryptUnprotectData")
)

// EncryptDPAPI encrypts plaintext data using Windows DPAPI (CryptProtectData).
// The encrypted blob is tied to the current Windows user credential context.
func EncryptDPAPI(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("dpapi: empty payload")
	}
	var inBlob = windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
	var outBlob windows.DataBlob
	r1, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0,
		uintptr(windows.CRYPTPROTECT_UI_FORBIDDEN),
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("dpapi protect error: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	cipher := make([]byte, outBlob.Size)
	copy(cipher, unsafe.Slice(outBlob.Data, outBlob.Size))
	return cipher, nil
}

// DecryptDPAPI decrypts ciphertext encrypted with EncryptDPAPI using Windows DPAPI (CryptUnprotectData).
func DecryptDPAPI(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("dpapi: empty ciphertext")
	}
	var inBlob = windows.DataBlob{
		Size: uint32(len(cipher)),
		Data: &cipher[0],
	}
	var outBlob windows.DataBlob
	r1, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0,
		uintptr(windows.CRYPTPROTECT_UI_FORBIDDEN),
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("dpapi unprotect error: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data)))

	plain := make([]byte, outBlob.Size)
	copy(plain, unsafe.Slice(outBlob.Data, outBlob.Size))
	return plain, nil
}
