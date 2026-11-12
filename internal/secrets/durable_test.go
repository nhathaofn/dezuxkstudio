package secrets

import (
	"errors"
	"os"
	"path/filepath"
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
	if _, err := keyring.Get("test", chunkKey("big", 0)); err == nil {
		t.Fatal("stale chunk left behind")
	}
	_ = k.Delete("big")
	if _, err := k.Get("big"); !errors.Is(err, ErrNotFound) {
		t.Fatal("not deleted")
	}
}
