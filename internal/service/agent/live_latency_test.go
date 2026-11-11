package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
	"app/internal/engine"
	"app/internal/tools/watermark"
)

// TestLiveAgyLatency prints a timeline of each chat event for simple questions (DEZUXK_LIVE_AGY=1).
func TestLiveAgyLatency(t *testing.T) {
	if os.Getenv("DEZUXK_LIVE_AGY") != "1" {
		t.Skip("set DEZUXK_LIVE_AGY=1")
	}
	profiles := os.Getenv("DEZUXK_LIVE_AGY_PROFILES")
	if profiles == "" {
		profiles = filepath.Join(os.Getenv("LOCALAPPDATA"), "DezuxkStudio", "data", "agent-profiles")
	}
	base := t.TempDir()
	reg := engine.NewRegistry()
	_ = reg.Register(watermark.NewNode(watermark.Options{}))

	var mu sync.Mutex
	var start time.Time
	var timeline []string
	doneCh := make(chan struct{}, 4)
	svc, err := New(Options{
		Repo: newFakeRepo(), DataDir: base, WorkspaceDir: filepath.Join(base, "ws"), MediaDir: filepath.Join(base, "media"),
		ProfilesDir: profiles, Logger: slog.New(slog.DiscardHandler), Workflow: registryWorkflow{reg: reg},
		Binaries: fakeBinaries{}, Preferences: fakePrefs{}, System: fakeSys{dir: base}, Diagnostics: fakeDiag{},
		OnChatEvent: func(e domainagent.ChatEvent) {
			mu.Lock()
			txt := strings.ReplaceAll(firstLineTrimmedMax(e.Text+e.Tool+e.Error, 70), "\n", " ")
			timeline = append(timeline, time.Since(start).Truncate(100*time.Millisecond).String()+"  "+string(e.Type)+"  "+txt)
			mu.Unlock()
			if e.Type == domainagent.ChatEventDone || e.Type == domainagent.ChatEventError {
				doneCh <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	t0 := time.Now()
	if _, err := svc.Connect(ctx, domainagent.ProviderAntigravity); err != nil {
		t.Fatal(err)
	}
	t.Logf("connect: %s", time.Since(t0))

	for _, q := range []string{"Chào bạn", "Bạn làm được gì trong ứng dụng này?", "Ứng dụng có tính năng xoá watermark không?"} {
		mu.Lock()
		timeline = nil
		start = time.Now()
		mu.Unlock()
		if _, _, err := svc.SendPrompt(ctx, q); err != nil {
			t.Fatal(err)
		}
		<-doneCh
		mu.Lock()
		t.Logf("\n### %s (total %s)\n%s", q, time.Since(start).Truncate(100*time.Millisecond), strings.Join(timeline, "\n"))
		mu.Unlock()
	}
}

// TestLiveModelSelection checks that model lists come from the real CLIs and that switching works (DEZUXK_LIVE_AGY=1).
func TestLiveModelSelection(t *testing.T) {
	if os.Getenv("DEZUXK_LIVE_AGY") != "1" {
		t.Skip("set DEZUXK_LIVE_AGY=1")
	}
	profiles := filepath.Join(os.Getenv("LOCALAPPDATA"), "DezuxkStudio", "data", "agent-profiles")
	base := t.TempDir()
	var mu sync.Mutex
	var first time.Time
	var start time.Time
	doneCh := make(chan struct{}, 4)
	svc, err := New(Options{
		Repo: newFakeRepo(), DataDir: base, WorkspaceDir: filepath.Join(base, "ws"), MediaDir: filepath.Join(base, "media"),
		ProfilesDir: profiles, Logger: slog.New(slog.DiscardHandler),
		OnChatEvent: func(e domainagent.ChatEvent) {
			mu.Lock()
			if e.Type == domainagent.ChatEventDelta && first.IsZero() {
				first = time.Now()
			}
			mu.Unlock()
			if e.Type == domainagent.ChatEventDone || e.Type == domainagent.ChatEventError {
				doneCh <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()

	for _, id := range []domainagent.ProviderID{domainagent.ProviderCodex, domainagent.ProviderClaude} {
		info, err := svc.Probe(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, m := range info.Models {
			names = append(names, m.ID+"["+strings.Join(m.Efforts, "/")+"]")
		}
		t.Logf("%s models (%d): %s", id, len(info.Models), strings.Join(names, ", "))
		if info.ResolvedPath != "" && len(info.Models) == 0 {
			t.Errorf("%s: expected model list from CLI", id)
		}
	}

	info, err := svc.Connect(ctx, domainagent.ProviderAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range info.Models {
		ids = append(ids, m.ID)
	}
	t.Logf("antigravity models (%d), current=%s: %s", len(info.Models), info.CurrentModel, strings.Join(ids, ", "))
	if len(info.Models) == 0 {
		t.Fatal("expected Antigravity to report models on connect")
	}
	for _, model := range []string{"gemini-3.8-flash-low", "gemini-3.8-flash-medium"} {
		if _, ok := findModel(info.Models, model); !ok {
			t.Logf("model %s not offered on this account, skipping", model)
			continue
		}
		upd, err := svc.SetModel(ctx, domainagent.ProviderAntigravity, model, "")
		if err != nil || upd.CurrentModel != model {
			t.Fatalf("SetModel(%s): info.current=%s err=%v", model, upd.CurrentModel, err)
		}
		mu.Lock()
		first, start = time.Time{}, time.Now()
		mu.Unlock()
		if _, _, err := svc.SendPrompt(ctx, "Chào bạn"); err != nil {
			t.Fatal(err)
		}
		<-doneCh
		mu.Lock()
		t.Logf("%s: first visible text after %s, total %s", model, first.Sub(start).Truncate(100*time.Millisecond), time.Since(start).Truncate(100*time.Millisecond))
		mu.Unlock()
	}
}
