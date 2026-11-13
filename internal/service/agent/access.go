package agent

// Folder access: the user-granted trusted folders and the file guard for remote node runs.

import (
	"context"

	"app/internal/apperr"
)

// ListAllowedDirs returns all user-approved workspace directories.
func (s *Service) ListAllowedDirs(ctx context.Context) ([]string, error) {
	return s.repo.ListAllowedDirs(ctx)
}

// AddAllowedDir validates and persists a user-approved working folder for the agent.
func (s *Service) AddAllowedDir(ctx context.Context, dir string) ([]string, error) {
	validated, err := s.policy.ValidateAllowedDirCandidate(dir)
	if err != nil {
		return nil, apperr.Validation(err.Error())
	}
	if err := s.repo.AddAllowedDir(ctx, validated); err != nil {
		return nil, err
	}
	dirs, err := s.repo.ListAllowedDirs(ctx)
	if err != nil {
		return nil, err
	}
	s.policy.SetAllowedDirs(dirs)
	return dirs, nil
}

// RemoveAllowedDir revokes a user-approved working folder.
func (s *Service) RemoveAllowedDir(ctx context.Context, dir string) ([]string, error) {
	if err := s.repo.RemoveAllowedDir(ctx, dir); err != nil {
		return nil, err
	}
	dirs, err := s.repo.ListAllowedDirs(ctx)
	if err != nil {
		return nil, err
	}
	s.policy.SetAllowedDirs(dirs)
	return dirs, nil
}

// CheckTrustedFile is the file guard for node runs from remote clients (see AccessPolicy.CheckTrustedFile).
// It follows the user's trusted-folder list as it changes.
func (s *Service) CheckTrustedFile(path string, write bool) error {
	return s.policy.CheckTrustedFile(path, write)
}
