package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"app/internal/secrets"
)

// API keys the user enters for an isolated provider profile (Anthropic, Gemini) are kept in the OS
// credential store, following internal/secrets' rule that secrets never sit in files. Each secret is
// addressed by the profile file path it used to live at, so profiles of different installs never collide.
// Keys saved as files by earlier builds are moved into the store the first time they are read.

// profileSecrets stores the API keys of the isolated provider profiles in the service's credential store.
type profileSecrets struct{ store secrets.Store }

func profileSecretKey(legacyPath string) secrets.Key {
	sum := sha256.Sum256([]byte(pathKey(legacyPath)))
	return secrets.Key("agent_api_key_" + hex.EncodeToString(sum[:8]))
}

// read returns the API key stored for legacyPath ("" when none).
func (ps profileSecrets) read(legacyPath string) string {
	store := ps.store
	key := profileSecretKey(legacyPath)
	if v, err := store.Get(key); err == nil {
		return strings.TrimSpace(v)
	} else if !errors.Is(err, secrets.ErrNotFound) {
		return ""
	}
	// Migrate a key written as a plain file by an earlier build.
	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(raw))
	if v != "" && store.Set(key, v) == nil {
		_ = os.Remove(legacyPath)
	}
	return v
}

// write stores the API key for legacyPath and removes any legacy plain-text copy.
func (ps profileSecrets) write(legacyPath, value string) error {
	if err := ps.store.Set(profileSecretKey(legacyPath), strings.TrimSpace(value)); err != nil {
		return err
	}
	_ = os.Remove(legacyPath)
	return nil
}

// remove deletes the API key for legacyPath from the store and disk.
func (ps profileSecrets) remove(legacyPath string) {
	_ = ps.store.Delete(profileSecretKey(legacyPath))
	_ = os.Remove(legacyPath)
}
