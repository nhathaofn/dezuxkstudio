package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"

	domainagent "app/internal/domain/agent"
	"app/internal/secrets"
	providersvc "app/internal/service/provider"
)

// ProviderLinkStore aliases domainagent.ProviderLinkRepository for opt-in agent provider links.
type ProviderLinkStore = domainagent.ProviderLinkRepository

// MediaAssetMetadata holds safe metadata of a media asset without raw paths or tickets.
type MediaAssetMetadata struct {
	AssetID        string         `json:"asset_id"`
	Kind           string         `json:"kind"`
	SourceNode     string         `json:"source_node,omitempty"`
	RegisteredRoot string         `json:"registered_root,omitempty"`
	MIMEType       string         `json:"mime_type"`
	SizeBytes      int64          `json:"size_bytes"`
	Width          int            `json:"width,omitempty"`
	Height         int            `json:"height,omitempty"`
	DurationSec    float64        `json:"duration_sec,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// MediaAssetInspector safely inspects metadata of a media asset by asset ID.
type MediaAssetInspector interface {
	InspectAsset(ctx context.Context, assetID string) (*MediaAssetMetadata, error)
}

var (
	serviceLinksMu      sync.RWMutex
	serviceLinks        = make(map[*Service]ProviderLinkStore)
	mediaInspectMu      sync.RWMutex
	mediaInspectors     = make(map[*Service]MediaAssetInspector)
	activeLinkSecretsMu sync.RWMutex
	activeLinkSecrets   = make(map[*Service]*providersvc.ResolvedLink)
)

// SetProviderLinks assigns the provider link store for an agent service.
func (s *Service) SetProviderLinks(store ProviderLinkStore) {
	serviceLinksMu.Lock()
	defer serviceLinksMu.Unlock()
	if store == nil {
		delete(serviceLinks, s)
	} else {
		serviceLinks[s] = store
	}
}

func (s *Service) providerLinksStore() ProviderLinkStore {
	serviceLinksMu.RLock()
	st := serviceLinks[s]
	serviceLinksMu.RUnlock()
	if st != nil {
		return st
	}
	if pls, ok := s.repo.(ProviderLinkStore); ok {
		return pls
	}
	return nil
}

// SetMediaInspector assigns the media asset inspector for safe metadata reading.
func (s *Service) SetMediaInspector(inspector MediaAssetInspector) {
	mediaInspectMu.Lock()
	defer mediaInspectMu.Unlock()
	if inspector == nil {
		delete(mediaInspectors, s)
	} else {
		mediaInspectors[s] = inspector
	}
}

func (s *Service) mediaInspectorService() MediaAssetInspector {
	mediaInspectMu.RLock()
	defer mediaInspectMu.RUnlock()
	return mediaInspectors[s]
}

func (s *Service) setActiveLinkedSecret(link *providersvc.ResolvedLink) {
	activeLinkSecretsMu.Lock()
	defer activeLinkSecretsMu.Unlock()
	if link == nil {
		delete(activeLinkSecrets, s)
	} else {
		activeLinkSecrets[s] = link
	}
}

func (s *Service) activeLinkedSecret() *providersvc.ResolvedLink {
	activeLinkSecretsMu.RLock()
	defer activeLinkSecretsMu.RUnlock()
	return activeLinkSecrets[s]
}

// RedactSecret replaces every occurrence of secret in text with "[REDACTED]".
func RedactSecret(text, secret string) string {
	secret = strings.TrimSpace(secret)
	if len(secret) < 6 || text == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[REDACTED]")
}

// RedactSecrets replaces every occurrence of any secret in text with "[REDACTED]".
func RedactSecrets(text string, secrets ...string) string {
	for _, sec := range secrets {
		text = RedactSecret(text, sec)
	}
	return text
}

// RedactError scrubs known secret values from an error's message.
func RedactError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	scrubbed := RedactSecrets(msg, secrets...)
	if scrubbed == msg {
		return err
	}
	return errors.New(scrubbed)
}

func (s *Service) redactTurnError(err error) error {
	if err == nil {
		return nil
	}
	var extra []string
	if active := s.activeLinkedSecret(); active != nil && active.Secret != "" {
		extra = append(extra, active.Secret)
	}
	scrub := func(in string) string {
		if in == "" {
			return ""
		}
		out := redactSecrets(in)
		if len(extra) > 0 {
			out = RedactSecrets(out, extra...)
		}
		return out
	}
	var le *localizedError
	if errors.As(err, &le) && le != nil {
		origMsg := le.Error()
		cleanMsg := scrub(origMsg)
		var cleanParams map[string]string
		paramsChanged := false
		if len(le.params) > 0 {
			cleanParams = make(map[string]string, len(le.params))
			for k, v := range le.params {
				cv := scrub(v)
				if cv != v {
					paramsChanged = true
				}
				cleanParams[k] = cv
			}
		} else {
			cleanParams = le.params
		}
		if cleanMsg != origMsg || paramsChanged {
			return localize(errors.New(cleanMsg), le.key, cleanParams)
		}
		return err
	}
	origMsg := err.Error()
	cleanMsg := scrub(origMsg)
	if cleanMsg == origMsg {
		return err
	}
	return errors.New(cleanMsg)
}

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
