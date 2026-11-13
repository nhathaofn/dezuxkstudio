package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

type brokenStore struct{}

func (brokenStore) Get(Key) (string, error) { return "", errors.New("no keyring") }
func (brokenStore) Set(Key, string) error   { return errors.New("no keyring") }
func (brokenStore) Delete(Key) error        { return errors.New("no keyring") }

func TestEncryptedFileRoundTripAndNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	f, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("k", "sk-very-secret"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, encryptedFileName))
	if strings.Contains(string(raw), "sk-very-secret") {
		t.Fatal("secret written in plaintext")
	}
	g, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.Get("k"); err != nil || v != "sk-very-secret" {
		t.Fatalf("got %q %v", v, err)
	}
	if err := g.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestEncryptedFileEnvMasterKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(MasterKeyEnv, "one")
	f, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Set("k", "v")
	if _, err := os.Stat(filepath.Join(dir, masterKeyFileName)); !os.IsNotExist(err) {
		t.Fatal("master key file must not be created when env key is set")
	}
	t.Setenv(MasterKeyEnv, "two")
	if _, err := NewEncryptedFile(dir); err == nil {
		t.Fatal("expected decrypt failure with wrong master key")
	}
}

func TestDurableFallsBackToEncryptedFile(t *testing.T) {
	d := newDurable(brokenStore{}, t.TempDir())
	if d.Backend() != BackendEncryptedFile {
		t.Fatalf("backend = %s", d.Backend())
	}
	if err := d.Set("a", "b"); err != nil {
		t.Fatal(err)
	}
	if BackendOf(d) != BackendEncryptedFile {
		t.Fatal("BackendOf mismatch")
	}
}

func TestDurableFallsBackToMemory(t *testing.T) {
	d := newDurable(brokenStore{}, "")
	if d.Backend() != BackendMemory {
		t.Fatalf("backend = %s", d.Backend())
	}
}

func TestDurableMigratesFileIntoKeyring(t *testing.T) {
	dir := t.TempDir()
	f, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Set("x", "secret")
	primary := NewMemory()
	d := newDurable(primary, dir)
	if d.Backend() != BackendKeyring {
		t.Fatalf("backend = %s", d.Backend())
	}
	if v, _ := primary.Get("x"); v != "secret" {
		t.Fatal("secret not migrated")
	}
	g, _ := NewEncryptedFile(dir)
	if len(g.Keys()) != 0 {
		t.Fatal("file not emptied after migration")
	}
	if _, err := primary.Get(probeKey); !errors.Is(err, ErrNotFound) {
		t.Fatal("probe key left behind")
	}
}

func TestKeyringChunksLargeValues(t *testing.T) {
	keyring.MockInit()
	k := NewKeyring("test")
	big := strings.Repeat("x", keyringChunk*2+7)
	if err := k.Set("big", big); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Get("big"); err != nil || v != big {
		t.Fatalf("len %d %v", len(v), err)
	}
	if err := k.Set("big", "small"); err != nil {
		t.Fatal(err)
	}
	if v, _ := k.Get("big"); v != "small" {
		t.Fatal(v)
	}
	if v, err := keyring.Get("test", "big"); err != nil || v != "small" {
		t.Fatalf("head must hold the plain value: %q %v", v, err)
	}
	// overwrite big → big: the value read back is always the newest
	big2 := strings.Repeat("y", keyringChunk*3)
	_ = k.Set("big", big)
	_ = k.Set("big", big2)
	if v, _ := k.Get("big"); v != big2 {
		t.Fatal("overwrite lost")
	}
	// a head pointing at missing chunks reads as not found (user re-enters the key)
	_ = keyring.Set("test", "broken", chunkMarker+"zz:2")
	if _, err := k.Get("broken"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	_ = k.Delete("big")
	if _, err := k.Get("big"); !errors.Is(err, ErrNotFound) {
		t.Fatal("not deleted")
	}
}

func TestEncryptedFileDPAPIProtectionAndMigration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI only")
	}
	dir := t.TempDir()
	f, err := NewEncryptedFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Set("k", "secret-val")

	rawKey, err := os.ReadFile(filepath.Join(dir, masterKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(rawKey), masterKeyDPAPIPrefix) {
		t.Fatalf("expected master.key to be DPAPI-protected, got %q", string(rawKey))
	}

	// Verify legacy plaintext master.key still loads and automatically upgrades to DPAPI.
	legacyDir := t.TempDir()
	rawPlain := make([]byte, 32)
	for i := range rawPlain {
		rawPlain[i] = byte(i + 1)
	}
	// Write a legacy base64 key first, store encrypted data using it, then reopen to verify upgrade.
	if err := os.WriteFile(filepath.Join(legacyDir, masterKeyFileName), []byte("AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="), 0o600); err != nil {
		t.Fatal(err)
	}
	lf, err := NewEncryptedFile(legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lf.Set("legacy", "val123"); err != nil {
		t.Fatal(err)
	}
	upgraded, err := os.ReadFile(filepath.Join(legacyDir, masterKeyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(upgraded), masterKeyDPAPIPrefix) {
		t.Fatalf("expected legacy master.key to migrate to DPAPI, got %q", string(upgraded))
	}
	reopened, err := NewEncryptedFile(legacyDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.Get("legacy"); err != nil || got != "val123" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
