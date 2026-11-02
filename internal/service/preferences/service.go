// Package preferences implements the preference use cases on top of the domain model.
package preferences

import (
	"context"
	"errors"
	"sync"

	"app/internal/domain/preferences"
	"app/internal/secrets"
)

// Service reads and updates user preferences. It is safe for concurrent use.
type Service struct {
	repo     preferences.Repository
	secrets  secrets.Store
	onChange func(preferences.Preferences)
	mu       sync.Mutex
}

// New creates the service. onChange (optional) is called after every successful update.
func New(repo preferences.Repository, sec secrets.Store, onChange func(preferences.Preferences)) (*Service, error) {
	if repo == nil {
		return nil, errors.New("preferences: repository is required")
	}
	if onChange == nil {
		onChange = func(preferences.Preferences) {}
	}
	return &Service{repo: repo, secrets: sec, onChange: onChange}, nil
}

// Get returns the stored preferences, migrated to the current schema, or the defaults.
func (s *Service) Get(ctx context.Context) (preferences.Preferences, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

// Update applies patch, persists the result and notifies listeners.
func (s *Service) Update(ctx context.Context, patch preferences.Patch) (preferences.Preferences, error) {
	s.mu.Lock()
	current, err := s.load(ctx)
	if err != nil {
		s.mu.Unlock()
		return preferences.Preferences{}, err
	}
	next, err := current.Apply(patch)
	if err != nil {
		s.mu.Unlock()
		return preferences.Preferences{}, err
	}
	if next != current {
		if err := s.repo.Save(ctx, next); err != nil {
			s.mu.Unlock()
			return preferences.Preferences{}, err
		}
	}
	s.mu.Unlock()

	if next != current {
		s.onChange(next)
	}
	return next, nil
}

func (s *Service) load(ctx context.Context) (preferences.Preferences, error) {
	stored, found, err := s.repo.Load(ctx)
	if err != nil {
		return preferences.Preferences{}, err
	}
	if !found {
		return preferences.Defaults(), nil
	}
	migrated, err := preferences.Migrate(stored)
	if err != nil {
		return preferences.Preferences{}, err
	}
	if migrated != stored {
		if err := s.repo.Save(ctx, migrated); err != nil {
			return preferences.Preferences{}, err
		}
	}
	return migrated, nil
}

// HasGatewayAPIKey reports whether a non-empty gateway API key is stored.
func (s *Service) HasGatewayAPIKey() bool {
	if s.secrets == nil {
		return false
	}
	v, err := s.secrets.Get(secrets.KeyGatewayAPIKey)
	return err == nil && v != ""
}

// SetGatewayAPIKey saves or deletes the gateway API key in the secrets store.
func (s *Service) SetGatewayAPIKey(key string) error {
	if s.secrets == nil {
		return errors.New("preferences: secret store not configured")
	}
	if key == "" {
		return s.secrets.Delete(secrets.KeyGatewayAPIKey)
	}
	return s.secrets.Set(secrets.KeyGatewayAPIKey, key)
}

// NotifyChange invokes onChange callback with current preferences and key state.
func (s *Service) NotifyChange(p preferences.Preferences) {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn(p)
	}
}
