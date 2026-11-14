package agent

import (
	"log/slog"
	"testing"

	"app/internal/config"
)

func TestLimitsFromAndDefaultsSubagentCap(t *testing.T) {
	defCfg := config.Default(config.ModeDesktop)
	defLimits := DefaultLimits()
	if defLimits.MaxConcurrentSubagents != defCfg.Agent.MaxConcurrentSubagents {
		t.Fatalf("DefaultLimits().MaxConcurrentSubagents = %d, want %d",
			defLimits.MaxConcurrentSubagents, defCfg.Agent.MaxConcurrentSubagents)
	}

	customCfg := config.Default(config.ModeDesktop)
	customCfg.Agent.MaxConcurrentSubagents = 9
	got := LimitsFrom(customCfg)
	if got.MaxConcurrentSubagents != 9 {
		t.Fatalf("LimitsFrom custom MaxConcurrentSubagents = %d, want 9", got.MaxConcurrentSubagents)
	}

	svc, err := New(Options{
		Repo:   newFakeRepo(),
		Logger: slog.New(slog.DiscardHandler),
		Limits: got,
	})
	if err != nil {
		t.Fatalf("agent.New failed: %v", err)
	}
	defer func() { _ = svc.Close() }()
	if cap(svc.subagentSem) != 9 {
		t.Fatalf("subagentSem capacity = %d, want 9", cap(svc.subagentSem))
	}

	// Zero and negative values fall back to config.Default(...) via withDefaults.
	for _, raw := range []int{0, -3} {
		fb := (Limits{MaxConcurrentSubagents: raw}).withDefaults()
		if fb.MaxConcurrentSubagents != defCfg.Agent.MaxConcurrentSubagents {
			t.Fatalf("withDefaults(%d).MaxConcurrentSubagents = %d, want %d",
				raw, fb.MaxConcurrentSubagents, defCfg.Agent.MaxConcurrentSubagents)
		}
	}

	preserved := (Limits{MaxConcurrentSubagents: 7}).withDefaults()
	if preserved.MaxConcurrentSubagents != 7 {
		t.Fatalf("withDefaults(7).MaxConcurrentSubagents = %d, want 7", preserved.MaxConcurrentSubagents)
	}
}
