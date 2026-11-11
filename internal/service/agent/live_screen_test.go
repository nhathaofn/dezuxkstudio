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

// TestLiveScreenContextAndGroups (DEZUXK_LIVE_AGY=1): capability groups, "these images" from the screen, follow-ups.
func TestLiveScreenContextAndGroups(t *testing.T) {
	if os.Getenv("DEZUXK_LIVE_AGY") != "1" {
		t.Skip("set DEZUXK_LIVE_AGY=1")
	}
	profiles := filepath.Join(os.Getenv("LOCALAPPDATA"), "DezuxkStudio", "data", "agent-profiles")
	base := t.TempDir()
	home, _ := os.UserHomeDir()
	userDir, err := os.MkdirTemp(home, "dezuxk-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(userDir) })
	a, b, c := filepath.Join(userDir, "a.png"), filepath.Join(userDir, "b.png"), filepath.Join(userDir, "sach.png")
	writeLiveImage(t, a, true)
	writeLiveImage(t, b, true)
	writeLiveImage(t, c, false)

	reg := engine.NewRegistry()
	_ = reg.Register(watermark.NewNode(watermark.Options{}))
	var mu sync.Mutex
	var reply strings.Builder
	var tools []string
	done := make(chan struct{}, 4)
	svc, err := New(Options{
		Repo: newFakeRepo(), DataDir: base, WorkspaceDir: filepath.Join(base, "ws"), MediaDir: filepath.Join(base, "media"),
		ProfilesDir: profiles, Logger: slog.New(slog.DiscardHandler), Workflow: registryWorkflow{reg: reg},
		Binaries: fakeBinaries{}, Preferences: fakePrefs{}, System: fakeSys{dir: base}, Diagnostics: fakeDiag{},
		OnChatEvent: func(e domainagent.ChatEvent) {
			mu.Lock()
			switch e.Type {
			case domainagent.ChatEventDelta:
				reply.WriteString(e.Text)
			case domainagent.ChatEventToolCall:
				tools = append(tools, e.Tool)
			}
			mu.Unlock()
			if e.Type == domainagent.ChatEventDone || e.Type == domainagent.ChatEventError {
				done <- struct{}{}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	ctx := context.Background()
	if _, err := svc.Connect(ctx, domainagent.ProviderAntigravity); err != nil {
		t.Fatal(err)
	}
	turn := func(q string) (string, []string) {
		mu.Lock()
		reply.Reset()
		tools = nil
		mu.Unlock()
		start := time.Now()
		if _, _, err := svc.SendPrompt(ctx, q); err != nil {
			t.Fatal(err)
		}
		<-done
		mu.Lock()
		defer mu.Unlock()
		t.Logf("\n===== %s (%s)\nTOOLS: %v\n%s\n", q, time.Since(start).Truncate(100*time.Millisecond), tools, reply.String())
		return reply.String(), append([]string(nil), tools...)
	}

	svc.SetUIContext(UIContext{Route: "/", Screen: "Tổng quan"})
	turn("Hiện tại ứng dụng đang có những tính năng nào?")

	svc.SetUIContext(UIContext{
		Route: "/watermark", Screen: "Watermark", Feature: watermark.NodeType,
		SelectedFiles: []string{a, b, c},
		Settings:      map[string]string{"corner": "bottom-right", "workers": "0", "inplace": "false", "output_dir": ""},
	})
	_, used := turn("Xoá watermark mấy ảnh này giúp mình.")
	ran := false
	for _, tl := range used {
		if strings.Contains(tl, "workflow_run_node") {
			ran = true
		}
	}
	if !ran {
		t.Error("expected the agent to run the feature on the screen's files without asking")
	}
	for _, out := range []string{"a_nowm.png", "b_nowm.png"} {
		if _, err := os.Stat(filepath.Join(userDir, out)); err != nil {
			t.Errorf("expected %s to be produced", out)
		}
	}

	svc.SetUIContext(UIContext{
		Route: "/watermark", Screen: "Watermark", Feature: watermark.NodeType, SelectedFiles: []string{a, b, c},
		Settings:   map[string]string{"corner": "bottom-right", "workers": "0", "inplace": "false", "output_dir": ""},
		LastResult: "2/3 images had a watermark and were cleaned; outputs: " + filepath.Join(userDir, "a_nowm.png") + ", " + filepath.Join(userDir, "b_nowm.png"),
	})
	turn("Ảnh nào không có watermark vậy?")
}
