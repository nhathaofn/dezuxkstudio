// Package preferences implements the preference use cases on top of the domain model.
package preferences

import (
	"context"
	"errors"
	"sync"

	"app/internal/domain/preferences"
)

// Service reads and updates user preferences. It is safe for concurrent use.
type Service struct {
	repo     preferences.Repository
	onChange func(preferences.Preferences)
	mu       sync.Mutex
}

// New creates the service. onChange (optional) is called after every successful update.
func New(repo preferences.Repository, onChange func(preferences.Preferences)) (*Service, error) {
	if repo == nil {
		return nil, errors.New("preferences: repository is required")
	}
	if onChange == nil {
		onChange = func(preferences.Preferences) {}
	}
	return &Service{repo: repo, onChange: onChange}, nil
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
