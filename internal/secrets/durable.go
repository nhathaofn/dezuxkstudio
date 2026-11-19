package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"app/internal/fsutil"
	"app/internal/security"
)

// Backend names where secrets are actually persisted. The UI shows a warning for anything but keyring.
type Backend string

const (
	// BackendKeyring: OS credential store (Windows Credential Manager, macOS Keychain, Secret Service).
	BackendKeyring Backend = "keyring"
	// BackendEncryptedFile: AES-GCM encrypted file in the data dir; used where no OS credential store works.
	BackendEncryptedFile Backend = "encrypted_file"
	// BackendMemory: process memory only; secrets are lost on restart.
	BackendMemory Backend = "memory"
	// BackendError: persistent fallback store on disk failed to open/decrypt; refusing silent memory fallback.
	BackendError Backend = "error"
)

// Backends lists every backend.
func Backends() []Backend {
	return []Backend{BackendKeyring, BackendEncryptedFile, BackendMemory, BackendError}
}

// MasterKeyEnv optionally supplies the key that encrypts the fallback secrets file. When unset, a random
// key is generated once and kept in a 0600 file next to the secrets file.
const MasterKeyEnv = "DEZUXK_MASTER_KEY"

const (
	encryptedFileName     = "secrets.enc"
	masterKeyFileName     = "master.key"
	probeKey          Key = "__dezuxk_probe__"
)

// BackendReporter is implemented by stores that know where they persist secrets.
type BackendReporter interface {
	Backend() Backend
}

// BackendOf reports the persistence backend of store (keyring for stores that do not say).
func BackendOf(store Store) Backend {
	if r, ok := store.(BackendReporter); ok {
		return r.Backend()
	}
	if _, ok := store.(*Memory); ok {
		return BackendMemory
	}
	return BackendKeyring
}

// Durable picks the strongest working backend once at start-up: OS keyring → encrypted file → memory.
// When the keyring works, secrets left in the encrypted file by an earlier run are moved into it.
type Durable struct {
	store   Store
	backend Backend
	err     error
}

type errorStore struct{ err error }

func (e *errorStore) Get(Key) (string, error) { return "", e.err }
func (e *errorStore) Set(Key, string) error   { return e.err }
func (e *errorStore) Delete(Key) error        { return e.err }

// Dir is the folder of the encrypted-file fallback inside the app data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "secrets") }

// NewDurableStore builds the application secret store. dir is the app data directory used by the
// encrypted-file fallback.
func NewDurableStore(service, dir string) *Durable {
	return newDurable(NewKeyring(service), dir)
}

func newDurable(primary Store, dir string) *Durable {
	if probe(primary) == nil {
		// Probe primary keyring before touching disk; only open the encrypted file if one already exists to migrate.
		if dir != "" {
			if _, statErr := os.Stat(filepath.Join(dir, encryptedFileName)); statErr == nil {
				if file, fileErr := NewEncryptedFile(dir); fileErr == nil {
					migrate(file, primary)
				}
			}
		}
		return &Durable{store: primary, backend: BackendKeyring}
	}
	if dir == "" {
		return &Durable{store: NewMemory(), backend: BackendMemory}
	}
	file, fileErr := NewEncryptedFile(dir)
	if fileErr == nil {
		return &Durable{store: file, backend: BackendEncryptedFile}
	}
	return &Durable{store: &errorStore{err: fileErr}, backend: BackendError, err: fileErr}
}

func probe(s Store) error {
	const want = "ok"
	if err := s.Set(probeKey, want); err != nil {
		return err
	}
	got, err := s.Get(probeKey)
	_ = s.Delete(probeKey)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("secrets: probe mismatch")
	}
	return nil
}

func migrate(from *EncryptedFile, to Store) {
	for _, k := range from.Keys() {
		v, err := from.Get(k)
		if err != nil {
			continue
		}
		if to.Set(k, v) == nil {
			_ = from.Delete(k)
		}
	}
}

func (d *Durable) Backend() Backend                { return d.backend }
func (d *Durable) Err() error                      { return d.err }
func (d *Durable) Get(key Key) (string, error)     { return d.store.Get(key) }
func (d *Durable) Set(key Key, value string) error { return d.store.Set(key, value) }
func (d *Durable) Delete(key Key) error            { return d.store.Delete(key) }

const (
	pbkdf2Iterations = 210_000
	pbkdf2SaltBytes  = 16
)

// EncryptedFile stores all secrets in one AES-256-GCM encrypted JSON file. It is weaker than the OS
// keyring (the key sits next to the data unless MasterKeyEnv is set) and is only used as a fallback.
type EncryptedFile struct {
	mu   sync.Mutex
	path string
	aead cipher.AEAD
	salt []byte
	data map[Key]string
}

type encryptedEnvelope struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt,omitempty"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	return aead, nil
}

// NewEncryptedFile opens (or prepares) the encrypted secrets file in dir.
func NewEncryptedFile(dir string) (*EncryptedFile, error) {
	if dir == "" {
		return nil, errors.New("secrets: no directory for encrypted store")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secrets: prepare dir: %w", err)
	}

	f := &EncryptedFile{
		path: filepath.Join(dir, encryptedFileName),
		data: map[Key]string{},
	}

	envMaster := strings.TrimSpace(os.Getenv(MasterKeyEnv))
	if envMaster != "" {
		needsWrite, err := f.loadWithEnvPassphrase(envMaster)
		if err != nil {
			return nil, err
		}
		if needsWrite {
			if err := f.save(); err != nil {
				return nil, err
			}
		}
		return f, nil
	}

	key, err := masterKey(dir)
	if err != nil {
		return nil, err
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	f.aead = aead
	if err := f.load(); err != nil {
		return nil, err
	}
	// Prove the directory is writable now rather than on the user's first save.
	if err := f.save(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *EncryptedFile) loadWithEnvPassphrase(passphrase string) (needsSave bool, err error) {
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		salt := make([]byte, pbkdf2SaltBytes)
		if _, rerr := rand.Read(salt); rerr != nil {
			return false, fmt.Errorf("secrets: generate salt: %w", rerr)
		}
		key, kerr := pbkdf2.Key(sha256.New, passphrase, salt, pbkdf2Iterations, 32)
		if kerr != nil {
			return false, fmt.Errorf("secrets: derive key: %w", kerr)
		}
		aead, aerr := newGCM(key)
		if aerr != nil {
			return false, aerr
		}
		f.salt = salt
		f.aead = aead
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("secrets: read store: %w", err)
	}
	var env encryptedEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false, errors.New("secrets: encrypted store is damaged")
	}
	var key []byte
	migrating := false
	if env.Salt != "" {
		salt, decErr := base64.StdEncoding.DecodeString(env.Salt)
		if decErr != nil || len(salt) != pbkdf2SaltBytes {
			return false, errors.New("secrets: encrypted store is damaged")
		}
		derived, kerr := pbkdf2.Key(sha256.New, passphrase, salt, pbkdf2Iterations, 32)
		if kerr != nil {
			return false, fmt.Errorf("secrets: derive key: %w", kerr)
		}
		key = derived
		f.salt = salt
	} else {
		// Legacy unsalted SHA-256 envelope: decrypt then upgrade to salted PBKDF2.
		sum := sha256.Sum256([]byte(passphrase))
		key = sum[:]
		migrating = true
	}
	aead, err := newGCM(key)
	if err != nil {
		return false, err
	}
	f.aead = aead
	if err := f.decryptEnvelope(env); err != nil {
		return false, err
	}
	if migrating {
		newSalt := make([]byte, pbkdf2SaltBytes)
		if _, rerr := rand.Read(newSalt); rerr != nil {
			return false, fmt.Errorf("secrets: generate salt: %w", rerr)
		}
		newKey, kerr := pbkdf2.Key(sha256.New, passphrase, newSalt, pbkdf2Iterations, 32)
		if kerr != nil {
			return false, fmt.Errorf("secrets: derive key: %w", kerr)
		}
		newAEAD, aerr := newGCM(newKey)
		if aerr != nil {
			return false, aerr
		}
		f.salt = newSalt
		f.aead = newAEAD
		return true, nil
	}
	return false, nil
}

const masterKeyDPAPIPrefix = "dpapi:"

func masterKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, masterKeyFileName)
	if raw, err := os.ReadFile(path); err == nil {
		text := strings.TrimSpace(string(raw))
		if strings.HasPrefix(text, masterKeyDPAPIPrefix) {
			blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(text, masterKeyDPAPIPrefix))
			if err != nil {
				return nil, errors.New("secrets: master key file is damaged")
			}
			k, err := security.DecryptDPAPI(blob)
			if err != nil || len(k) != 32 {
				return nil, fmt.Errorf("secrets: decrypt master key: %w", err)
			}
			return k, nil
		}
		k, err := base64.StdEncoding.DecodeString(text)
		if err != nil || len(k) != 32 {
			return nil, errors.New("secrets: master key file is damaged")
		}
		// Migrate legacy plaintext base64 master.key to DPAPI protection on Windows.
		if encoded, dpErr := encodeMasterKey(k); dpErr == nil && strings.HasPrefix(encoded, masterKeyDPAPIPrefix) {
			_ = fsutil.WriteFileAtomic(path, []byte(encoded), 0o600)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secrets: read master key: %w", err)
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("secrets: generate master key: %w", err)
	}
	encoded, err := encodeMasterKey(k)
	if err != nil {
		return nil, err
	}
	if err := fsutil.WriteFileAtomic(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("secrets: write master key: %w", err)
	}
	return k, nil
}

func encodeMasterKey(k []byte) (string, error) {
	blob, err := security.EncryptDPAPI(k)
	if err == nil {
		return masterKeyDPAPIPrefix + base64.StdEncoding.EncodeToString(blob), nil
	}
	if errors.Is(err, security.ErrDPAPIUnsupported) {
		return base64.StdEncoding.EncodeToString(k), nil
	}
	return "", fmt.Errorf("secrets: protect master key: %w", err)
}

func (f *EncryptedFile) load() error {
	f.data = map[Key]string{}
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("secrets: read store: %w", err)
	}
	var env encryptedEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return errors.New("secrets: encrypted store is damaged")
	}
	return f.decryptEnvelope(env)
}

func (f *EncryptedFile) decryptEnvelope(env encryptedEnvelope) error {
	nonce, err1 := base64.StdEncoding.DecodeString(env.Nonce)
	ct, err2 := base64.StdEncoding.DecodeString(env.Ciphertext)
	if err1 != nil || err2 != nil || len(nonce) != f.aead.NonceSize() {
		return errors.New("secrets: encrypted store is damaged")
	}
	plain, err := f.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return errors.New("secrets: cannot decrypt store (wrong master key?)")
	}
	var m map[Key]string
	if err := json.Unmarshal(plain, &m); err != nil {
		return errors.New("secrets: encrypted store is damaged")
	}
	if m != nil {
		f.data = m
	}
	return nil
}

func (f *EncryptedFile) save() error {
	plain, err := json.Marshal(f.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("secrets: nonce: %w", err)
	}
	env := encryptedEnvelope{
		Version:    1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(f.aead.Seal(nil, nonce, plain, nil)),
	}
	if len(f.salt) > 0 {
		env.Version = 2
		env.Salt = base64.StdEncoding.EncodeToString(f.salt)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(f.path, raw, 0o600); err != nil {
		return fmt.Errorf("secrets: write store: %w", err)
	}
	return nil
}

func (f *EncryptedFile) Backend() Backend { return BackendEncryptedFile }

// Keys lists the stored secret names.
func (f *EncryptedFile) Keys() []Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Key, 0, len(f.data))
	for k := range f.data {
		out = append(out, k)
	}
	return out
}

func (f *EncryptedFile) Get(key Key) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *EncryptedFile) Set(key Key, value string) error {
	if value == "" {
		return f.Delete(key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	prev, had := f.data[key]
	f.data[key] = value
	if err := f.save(); err != nil {
		if had {
			f.data[key] = prev
		} else {
			delete(f.data, key)
		}
		return err
	}
	return nil
}

func (f *EncryptedFile) Delete(key Key) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev, had := f.data[key]
	if !had {
		return nil
	}
	delete(f.data, key)
	if err := f.save(); err != nil {
		f.data[key] = prev
		return err
	}
	return nil
}
