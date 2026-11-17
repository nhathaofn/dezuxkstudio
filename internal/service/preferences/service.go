// Package preferences implements the preference use cases on top of the domain model.
package preferences

import (
	"context"
	"errors"
	"sync"

	"app/internal/domain/preferences"
)

// WorkspaceRootValidator validates and optionally normalizes a candidate workspace root path.
type WorkspaceRootValidator func(raw string) (normalized string, err error)

// Option configures the Service.
type Option func(*Service)

// WithWorkspaceRootValidator configures the candidate workspace root validator.
func WithWorkspaceRootValidator(v WorkspaceRootValidator) Option {
	return func(s *Service) {
		s.validator = v
	}
}

// Service reads and updates user preferences. It is safe for concurrent use.
type Service struct {
	repo      preferences.Repository
	onChange  func(preferences.Preferences)
	validator WorkspaceRootValidator
	mu        sync.Mutex
}

// SetWorkspaceRootValidator sets or updates the candidate workspace root validator.
func (s *Service) SetWorkspaceRootValidator(v WorkspaceRootValidator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validator = v
}

// New creates the service. onChange (optional) is called after every successful update.
func New(repo preferences.Repository, onChange func(preferences.Preferences), opts ...Option) (*Service, error) {
	if repo == nil {
		return nil, errors.New("preferences: repository is required")
	}
	if onChange == nil {
		onChange = func(preferences.Preferences) {}
	}
	s := &Service{repo: repo, onChange: onChange}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s, nil
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
	if patch.WorkspaceRoot != nil && s.validator != nil {
		norm, err := s.validator(*patch.WorkspaceRoot)
		if err != nil {
			s.mu.Unlock()
			return preferences.Preferences{}, err
		}
		patch.WorkspaceRoot = &norm
	}
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
