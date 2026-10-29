// Package secrets stores credentials in the OS credential store
// (Windows Credential Manager, macOS Keychain, Secret Service on Linux).
// Secrets must never be written to config files, the database or logs.
package secrets

import (
	"errors"
	"fmt"
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
