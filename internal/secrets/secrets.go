// Package secrets stores credentials in the OS credential store
// (Windows Credential Manager, macOS Keychain, Secret Service on Linux).
// Secrets must never be written to config files, the database or logs.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when no secret exists for a key.
var ErrNotFound = errors.New("secrets: not found")

// Key names a secret slot. Declare every key as a constant here.
type Key string

const (
	// KeyRemoteAuthToken is the token used by client mode to reach a remote server.
	KeyRemoteAuthToken Key = "remote_auth_token"
)

// Store is the credential storage abstraction used by services.
type Store interface {
	Get(key Key) (string, error)
	Set(key Key, value string) error
	Delete(key Key) error
}

// Keyring implements Store with the OS credential store, scoped by service name.
type Keyring struct{ service string }

// NewKeyring creates a keyring-backed store; service is normally the AppID.
func NewKeyring(service string) *Keyring { return &Keyring{service: service} }

func (k *Keyring) Get(key Key) (string, error) {
	v, err := keyring.Get(k.service, string(key))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secrets: get %s: %w", key, err)
	}
	return v, nil
}

func (k *Keyring) Set(key Key, value string) error {
	if value == "" {
		return k.Delete(key)
	}
	if err := keyring.Set(k.service, string(key), value); err != nil {
		return fmt.Errorf("secrets: set %s: %w", key, err)
	}
	return nil
}

func (k *Keyring) Delete(key Key) error {
	err := keyring.Delete(k.service, string(key))
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("secrets: delete %s: %w", key, err)
	}
	return nil
}

// Memory is an in-process Store for tests and platforms without a credential store.
type Memory struct {
	mu   sync.Mutex
	data map[Key]string
}

// NewMemory creates an empty in-memory store.
func NewMemory() *Memory { return &Memory{data: map[Key]string{}} }

func (m *Memory) Get(key Key) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) Set(key Key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value == "" {
		delete(m.data, key)
		return nil
	}
	m.data[key] = value
	return nil
}

func (m *Memory) Delete(key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// HybridStore combines a primary store with a fallback store.
type HybridStore struct {
	primary  Store
	fallback Store
}

// NewHybridStore constructs a store that tries primary first, falling back to fallback if unavailable or failing.
func NewHybridStore(primary, fallback Store) *HybridStore {
	return &HybridStore{primary: primary, fallback: fallback}
}

func (h *HybridStore) Get(key Key) (string, error) {
	if h.primary != nil {
		v, err := h.primary.Get(key)
		if err == nil {
			return v, nil
		}
		if errors.Is(err, ErrNotFound) {
			if h.fallback != nil {
				return h.fallback.Get(key)
			}
			return "", ErrNotFound
		}
		// Primary failed with operational error (e.g. OS keyring unavailable)
		if h.fallback != nil {
			return h.fallback.Get(key)
		}
		return "", err
	}
	if h.fallback != nil {
		return h.fallback.Get(key)
	}
	return "", ErrNotFound
}

func (h *HybridStore) Set(key Key, value string) error {
	var primaryErr error
	if h.primary != nil {
		primaryErr = h.primary.Set(key, value)
		if primaryErr == nil {
			if h.fallback != nil {
				_ = h.fallback.Set(key, value)
			}
			return nil
		}
	}
	if h.fallback != nil {
		return h.fallback.Set(key, value)
	}
	return primaryErr
}

func (h *HybridStore) Delete(key Key) error {
	var primaryErr error
	if h.primary != nil {
		primaryErr = h.primary.Delete(key)
	}
	if h.fallback != nil {
		_ = h.fallback.Delete(key)
	}
	if primaryErr != nil && !errors.Is(primaryErr, ErrNotFound) {
		if h.fallback != nil {
			return nil
		}
		return primaryErr
	}
	return nil
}

// EnvStore reads key from an environment variable, with a fallback Store for writes and other keys.
type EnvStore struct {
	envVar   string
	key      Key
	fallback Store
}

// NewEnvStore creates a store that reads the specified key from envVar, delegating all else to fallback.
func NewEnvStore(key Key, envVar string, fallback Store) *EnvStore {
	return &EnvStore{key: key, envVar: envVar, fallback: fallback}
}

func (e *EnvStore) Get(key Key) (string, error) {
	if key == e.key && e.envVar != "" {
		if val := os.Getenv(e.envVar); val != "" {
			return val, nil
		}
	}
	if e.fallback != nil {
		return e.fallback.Get(key)
	}
	return "", ErrNotFound
}

func (e *EnvStore) Set(key Key, value string) error {
	if e.fallback != nil {
		return e.fallback.Set(key, value)
	}
	return nil
}

func (e *EnvStore) Delete(key Key) error {
	if e.fallback != nil {
		return e.fallback.Delete(key)
	}
	return nil
}

// NewDesktopStore creates an OS Keyring store with an in-memory fallback.
func NewDesktopStore(service string) Store {
	return NewHybridStore(NewKeyring(service), NewMemory())
}
