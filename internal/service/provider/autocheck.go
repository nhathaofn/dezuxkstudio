package provider

import (
	"context"
	"sync"
	"time"

	"app/internal/apperr"
	domain "app/internal/domain/provider"
	"app/internal/safego"
)

// Automatic verification. Whenever a key is saved, changed or made active (and when a custom provider is
// saved or edited) the service checks it in the background: is the key alive and, if the provider's API
// lists models, which ones. The result lands in the same places the manual Test / Refresh buttons use
// (health + model list) and each change is pushed to the UI, so nobody has to press a button.
//
// Nothing here knows a provider by name: what is checked, and whether models can be listed, comes from
// the registry (Meta.Probe), so every provider added to the registry later gets it for free.

type autoCheck struct {
	mu       sync.Mutex
	running  map[string]bool // owner id (credential or custom provider) -> a check is in flight
	rerun    map[string]bool // changed again while checking: check once more with the new data
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	finished func() // test hook: called after each completed check
}

func newAutoCheck() *autoCheck {
	ctx, cancel := context.WithCancel(context.Background())
	return &autoCheck{running: map[string]bool{}, rerun: map[string]bool{}, ctx: ctx, cancel: cancel}
}

// Close stops background checks and waits for them. Call it before closing the database.
func (s *Service) Close() {
	s.auto.cancel()
	s.auto.wg.Wait()
}

// Wait blocks until every background check has finished.
func (s *Service) Wait() { s.auto.wg.Wait() }

// isChecking reports whether a background check of id is in flight.
func (s *Service) isChecking(id string) bool {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()
	return s.auto.running[id]
}

// startAutoCheck verifies a credential or custom provider in the background.
func (s *Service) startAutoCheck(id string) {
	if !s.autoOn {
		return
	}
	s.auto.mu.Lock()
	if s.auto.running[id] {
		s.auto.rerun[id] = true // data changed while a check runs: go again afterwards
		s.auto.mu.Unlock()
		return
	}
	s.auto.running[id] = true
	s.auto.wg.Add(1)
	s.auto.mu.Unlock()
	s.changed() // the UI shows "checking" right away

	safego.Go("provider-auto-check", func() {
		defer s.auto.wg.Done()
		for {
			s.checkAndRefresh(s.auto.ctx, id)
			s.auto.mu.Lock()
			if s.auto.rerun[id] && s.auto.ctx.Err() == nil {
				s.auto.rerun[id] = false
				s.auto.mu.Unlock()
				continue
			}
			delete(s.auto.running, id)
			delete(s.auto.rerun, id)
			s.auto.mu.Unlock()
			break
		}
		if s.auto.ctx.Err() == nil {
			s.changed()
		}
		if s.auto.finished != nil {
			s.auto.finished()
		}
	})
}

// checkAndRefresh tests a credential (or custom provider) and, when it works, refreshes the model list
// from the provider's API. New models are added without touching the user's choices (MergeDiscovered).
// Only the provider's active key is used to list models; any other saved key is just tested.
func (s *Service) checkAndRefresh(ctx context.Context, id string) {
	if c, err := s.repo.GetCredential(ctx, id); err == nil {
		res, err := s.TestCredential(ctx, id)
		if err != nil || !res.OK() || !c.Active {
			return
		}
		if m, ok := domain.Lookup(c.ProviderID); ok && m.Discoverable() {
			if _, err := s.DiscoverBuiltinModels(ctx, c.ProviderID); err != nil && ctx.Err() == nil {
				_ = s.repo.SetCredentialHealth(ctx, id, modelsFailed(err))
			}
		}
		return
	}
	if _, err := s.repo.GetCustom(ctx, id); err == nil {
		if res, err := s.TestCustom(ctx, id); err == nil && res.OK() {
			if _, err := s.DiscoverModelsFor(ctx, id); err != nil && ctx.Err() == nil {
				_ = s.repo.SetCustomHealth(ctx, id, modelsFailed(err))
			}
		}
	}
}

// modelsFailed is the health of a key that works but whose model list could not be loaded. It is not
// "dead": the key is fine, so the user is told what is missing instead of seeing an empty list in silence.
func modelsFailed(err error) domain.Health {
	return domain.Health{Outcome: OutcomeModelsFailed, Detail: apperr.From(err).Message, TestedAt: time.Now()}
}
