//go:build windows

package security

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptDPAPI_Roundtrip(t *testing.T) {
	testCases := [][]byte{
		[]byte("hello world"),
		[]byte("{\n  \"cookies\": [{\"name\": \"SID\", \"value\": \"xyz123\"}]\n}"),
		[]byte("Tiếng Việt có dấu và ký tự đặc biệt !@#$%^&*()_+{}[]:;\"'<>?,./"),
	}

	for _, tc := range testCases {
		encrypted, err := EncryptDPAPI(tc)
		if err != nil {
			t.Fatalf("EncryptDPAPI failed: %v", err)
		}
		if len(encrypted) == 0 {
			t.Fatalf("EncryptDPAPI returned empty ciphertext")
		}
		if bytes.Equal(encrypted, tc) {
			t.Fatalf("Ciphertext equals plaintext, expected encryption")
		}

		decrypted, err := DecryptDPAPI(encrypted)
		if err != nil {
			t.Fatalf("DecryptDPAPI failed: %v", err)
		}
		if !bytes.Equal(decrypted, tc) {
			t.Fatalf("expected decrypted %s, got %s", string(tc), string(decrypted))
		}
	}
}

func TestEncryptDecryptDPAPI_LargePayload(t *testing.T) {
	// 512 KB random payload
	large := make([]byte, 512*1024)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}

	encrypted, err := EncryptDPAPI(large)
	if err != nil {
		t.Fatalf("EncryptDPAPI failed for large payload: %v", err)
	}
	decrypted, err := DecryptDPAPI(encrypted)
	if err != nil {
		t.Fatalf("DecryptDPAPI failed for large payload: %v", err)
	}
	if !bytes.Equal(decrypted, large) {
		t.Fatalf("decrypted large payload mismatch")
	}
}

func TestEncryptDecryptDPAPI_EmptyPayload(t *testing.T) {
	_, err := EncryptDPAPI(nil)
	if err == nil {
		t.Fatalf("expected error on empty data, got nil")
	}

	_, err = EncryptDPAPI([]byte{})
	if err == nil {
		t.Fatalf("expected error on empty slice, got nil")
	}

	_, err = DecryptDPAPI(nil)
	if err == nil {
		t.Fatalf("expected error on empty ciphertext, got nil")
	}

	_, err = DecryptDPAPI([]byte{})
	if err == nil {
		t.Fatalf("expected error on empty slice, got nil")
	}
}

func TestDecryptDPAPI_CorruptData(t *testing.T) {
	corrupt := []byte("invalid-encrypted-blob-content")
	_, err := DecryptDPAPI(corrupt)
	if err == nil {
		t.Fatalf("expected error decrypting corrupt data, got nil")
	}
}
