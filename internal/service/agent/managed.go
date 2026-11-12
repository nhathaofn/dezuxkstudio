package agent

import (
	"context"
	"sync"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/lookup"
	"app/internal/progress"
	"app/internal/safego"
)

// The app keeps its own copy of each agent CLI in its data folder (see service/agentcli). It is the
// preferred executable, so the app behaves the same on every machine and never depends on, changes or
// is changed by a CLI the user installed themselves; a copy found elsewhere is only the fallback.

// provisionState is what the UI shows while a managed copy is downloading.
type provisionState struct {
	active  bool
	percent float64
}

// managedFinder finds the app's own copy of id, or "" when there is none.
func (s *Service) managedFinder(id domainagent.ProviderID) lookup.Finder {
	return func() string {
		if s.managed == nil {
			return ""
		}
		if cur, ok := s.managed.Current(id); ok {
			return cur.Path
		}
		return ""
	}
}

// ProvisionCLIs installs the agent CLIs the app is missing and keeps the ones it has on the newest
// official release. It runs until ctx ends, looking again every Limits.CLIUpdateInterval. A failure
// (offline, server down) only skips that round; the CLI that works stays in use.
func (s *Service) ProvisionCLIs(ctx context.Context) {
	if s.managed == nil || (!s.autoInstall && !s.autoUpdate) {
		return
	}
	for {
		s.provisionRound(ctx)
		if !s.autoUpdate {
			return // nothing to look for later: a missing CLI was tried once
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.limits.CLIUpdateInterval):
		}
	}
}

func (s *Service) provisionRound(ctx context.Context) {
	var wg sync.WaitGroup
	for _, id := range domainagent.AllProviderIDs() {
		if !s.managed.Supported(id) {
			continue
		}
		_, has := s.managed.Current(id)
		if (has && !s.autoUpdate) || (!has && !s.autoInstall) {
			continue
		}
		wg.Add(1)
		safego.Go("agent-provision-"+string(id), func() {
			defer wg.Done()
			s.provision(ctx, id)
		})
	}
	wg.Wait()
}

// provision brings one provider's managed copy up to date and tells the UI what changed.
func (s *Service) provision(ctx context.Context, id domainagent.ProviderID) {
	s.setProvisioning(id, provisionState{active: true})
	s.emitStatusOf(ctx, id)

	var lastEmit float64
	sink := progress.Sink{Report: func(u progress.Update) {
		if u.Percent-lastEmit < 1 && u.Percent < 100 {
			return
		}
		lastEmit = u.Percent
		s.setProvisioning(id, provisionState{active: true, percent: u.Percent})
		s.emitStatusOf(ctx, id)
	}}
	cur, changed, err := s.managed.Install(progress.With(ctx, sink), id)

	s.setProvisioning(id, provisionState{})
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("agent: could not install or update the CLI", "provider", id, "error", err)
		}
	} else if changed {
		s.log.Info("agent: CLI is ready", "provider", id, "version", cur.Version)
		s.invalidateStatus(id) // the version and the sign-in state belong to the new executable
	}
	s.emitStatusOf(ctx, id)
}

func (s *Service) setProvisioning(id domainagent.ProviderID, st provisionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.active {
		s.provisioning[id] = st
	} else {
		delete(s.provisioning, id)
	}
}
