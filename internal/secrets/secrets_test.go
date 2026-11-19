package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurableDoesNotCreateFilesWhenKeyringWorks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	primary := NewMemory()
	d := newDurable(primary, dir)
	if d.Backend() != BackendKeyring {
		t.Fatalf("backend = %s, want keyring", d.Backend())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secrets dir must not be created when keyring works, stat err=%v", err)
	}
}

func TestDurableRefusesSilentMemoryFallbackOnCorruptEncryptedStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, encryptedFileName), []byte("corrupt-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDurable(brokenStore{}, dir)
	if d.Backend() != BackendError {
		t.Fatalf("backend = %s, want error", d.Backend())
	}
	if d.Err() == nil {
		t.Fatal("expected non-nil Err()")
	}
	if err := d.Set("k", "v"); err == nil {
		t.Fatal("expected Set to fail on damaged encrypted store instead of falling back to memory")
	}
	if _, err := d.Get("k"); err == nil {
		t.Fatal("expected Get to fail on damaged encrypted store")
	}
}

func TestEncryptedFileEnvMasterKeyUsesSaltedPBKDF2(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(MasterKeyEnv, "correct-horse-battery")
	f, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("k", "val"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, encryptedFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version":2`) || !strings.Contains(string(raw), `"salt":`) {
		t.Fatalf("expected version 2 salted envelope, got %s", string(raw))
	}
}
