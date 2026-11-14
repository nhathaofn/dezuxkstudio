package modules_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"app/internal/apperr"
	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	"app/internal/realtime"
	"app/internal/realtime/modules"
	agentsvc "app/internal/service/agent"
)

type stubAgentRepo struct {
	domainagent.Repository
	mu          sync.Mutex
	customPaths map[domainagent.ProviderID]string
	allowedDirs []string
}

func (r *stubAgentRepo) GetCustomPath(_ context.Context, id domainagent.ProviderID) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.customPaths[id], nil
}

func (r *stubAgentRepo) GetAllCustomPaths(_ context.Context) (map[domainagent.ProviderID]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[domainagent.ProviderID]string, len(r.customPaths))
	for k, v := range r.customPaths {
		cp[k] = v
	}
	return cp, nil
}

func (r *stubAgentRepo) SetCustomPath(_ context.Context, id domainagent.ProviderID, customPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.customPaths == nil {
		r.customPaths = make(map[domainagent.ProviderID]string)
	}
	r.customPaths[id] = customPath
	return nil
}

func (r *stubAgentRepo) ListAllowedDirs(context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]string, len(r.allowedDirs))
	copy(cp, r.allowedDirs)
	return cp, nil
}

func (r *stubAgentRepo) GetModelSelection(context.Context, domainagent.ProviderID) (domainagent.ModelSelection, error) {
	return domainagent.ModelSelection{}, nil
}

func (r *stubAgentRepo) SetRememberedConnection(context.Context, domainagent.ProviderID) error {
	return nil
}

func newTestAgentService(t *testing.T) (*agentsvc.Service, *stubAgentRepo) {
	t.Helper()
	base := t.TempDir()
	repo := &stubAgentRepo{}
	svc, err := agentsvc.New(agentsvc.Options{
		Repo:              repo,
		DataDir:           filepath.Join(base, "data"),
		WorkspaceDir:      filepath.Join(base, "ws"),
		MediaDir:          filepath.Join(base, "media"),
		ProfilesDir:       filepath.Join(base, "profiles"),
		CustomizationsDir: filepath.Join(base, "customizations"),
		Logger:            slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("agentsvc.New failed: %v", err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Errorf("agentsvc.Close: %v", err)
		}
	})
	return svc, repo
}

func assertUnavailableWithMessage(t *testing.T, action string, err error, wantMsg string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeUnavailable {
		t.Fatalf("%s: expected ERR_UNAVAILABLE, got %v", action, err)
	}
	if ae.Message != wantMsg {
		t.Fatalf("%s: expected message %q, got %q", action, wantMsg, ae.Message)
	}
}

func mustCall(t *testing.T, reg *realtime.Registry, action string, raw string) (any, error) {
	t.Helper()
	h, ok := reg.Lookup(action)
	if !ok {
		t.Fatalf("%s not registered", action)
	}
	return h(context.Background(), &realtime.Request{}, []byte(raw))
}

// In headless server mode a remote client must not widen the folders the workflow file guard trusts.
func TestAgentModule_LockedTrustedFoldersRefuseChanges(t *testing.T) {
	reg := realtime.NewRegistry(contracts.ActionNames())
	modules.Agent{LockTrustedFolders: true}.Register(reg) // Service never reached when locked

	const wantMsg = "trusted folders can only be changed in the desktop app on this machine"
	for _, action := range []string{contracts.ActionAgentAddAllowedDir, contracts.ActionAgentRemoveAllowedDir} {
		_, err := mustCall(t, reg, action, `{"dir":"C:/Users/someone/Documents"}`)
		assertUnavailableWithMessage(t, action, err, wantMsg)
	}
}

func TestAgentModule_HostAdminAndCustomizationsGuards(t *testing.T) {
	const (
		hostAdminMsg      = "host path and desktop folder actions are disabled in server mode"
		customizationsMsg = "agent skills and subagents cannot be modified remotely in server mode"
	)

	customizationCases := []struct {
		action  string
		payload string
	}{
		{
			action:  contracts.ActionAgentSaveSkill,
			payload: `{"name":"custom-skill","description":"Custom skill","user_invocable":true,"enabled":true,"body":"Step 1"}`,
		},
		{
			action:  contracts.ActionAgentDeleteSkill,
			payload: `{"name":"custom-skill"}`,
		},
		{
			action:  contracts.ActionAgentRestoreSkill,
			payload: `{"name":"brief-structuring"}`,
		},
		{
			action:  contracts.ActionAgentSaveSubagent,
			payload: `{"name":"custom-sub","description":"Custom sub","tools":[],"skills":[],"enabled":true,"body":"Inspect"}`,
		},
		{
			action:  contracts.ActionAgentDeleteSubagent,
			payload: `{"name":"custom-sub"}`,
		},
		{
			action:  contracts.ActionAgentRestoreSubagent,
			payload: `{"name":"workspace-reviewer"}`,
		},
	}

	t.Run("locked host admin and customizations refuse mutations with exact ERR_UNAVAILABLE messages", func(t *testing.T) {
		svc, _ := newTestAgentService(t)
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.Agent{
			Service:            svc,
			LockHostAdmin:      true,
			LockCustomizations: true,
		}.Register(reg)

		_, err := mustCall(t, reg, contracts.ActionAgentSetPath, `{"id":"antigravity","custom_path":"C:\\custom\\agy.exe"}`)
		assertUnavailableWithMessage(t, contracts.ActionAgentSetPath, err, hostAdminMsg)

		for _, tc := range customizationCases {
			_, err := mustCall(t, reg, tc.action, tc.payload)
			assertUnavailableWithMessage(t, tc.action, err, customizationsMsg)
		}

		// Read/sync customization actions remain unlocked.
		if _, err := mustCall(t, reg, contracts.ActionAgentListCustomizations, `{}`); err != nil {
			t.Fatalf("agent:list_customizations failed when locked: %v", err)
		}
		if _, err := mustCall(t, reg, contracts.ActionAgentReloadCustomizations, `{}`); err != nil {
			t.Fatalf("agent:reload_customizations failed when locked: %v", err)
		}
	})

	t.Run("LockHostAdmin and LockCustomizations act independently", func(t *testing.T) {
		svc, _ := newTestAgentService(t)
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.Agent{
			Service:            svc,
			LockHostAdmin:      true,
			LockCustomizations: false,
		}.Register(reg)

		_, err := mustCall(t, reg, contracts.ActionAgentSetPath, `{"id":"antigravity","custom_path":"C:\\custom\\agy.exe"}`)
		assertUnavailableWithMessage(t, contracts.ActionAgentSetPath, err, hostAdminMsg)

		if _, err := mustCall(t, reg, contracts.ActionAgentSaveSkill, `{"name":"independent-skill","description":"Works when only host admin is locked","user_invocable":true,"enabled":true,"body":"Body"}`); err != nil {
			t.Fatalf("expected agent:save_skill to succeed when LockCustomizations is false, got %v", err)
		}
	})

	t.Run("unlocked host admin and customizations forward to service", func(t *testing.T) {
		svc, repo := newTestAgentService(t)
		reg := realtime.NewRegistry(contracts.ActionNames())
		modules.Agent{
			Service:            svc,
			LockHostAdmin:      false,
			LockCustomizations: false,
		}.Register(reg)

		res, err := mustCall(t, reg, contracts.ActionAgentSetPath, `{"id":"antigravity","custom_path":"C:\\custom\\agy.exe"}`)
		if err != nil {
			t.Fatalf("agent:set_path failed: %v", err)
		}
		info, ok := res.(contracts.AgentProviderInfo)
		if !ok || info.CustomPath != `C:\custom\agy.exe` {
			t.Fatalf("unexpected AgentProviderInfo: %#v", res)
		}
		if got := repo.customPaths[domainagent.ProviderAntigravity]; got != `C:\custom\agy.exe` {
			t.Fatalf("expected repo custom path saved, got %q", got)
		}

		// Exercise save, delete, and restore for both skills and subagents.
		for _, tc := range []struct {
			action  string
			payload string
		}{
			{contracts.ActionAgentSaveSkill, `{"name":"mod-skill","description":"Mod skill","user_invocable":true,"enabled":true,"body":"Step 1"}`},
			{contracts.ActionAgentDeleteSkill, `{"name":"mod-skill"}`},
			{contracts.ActionAgentDeleteSkill, `{"name":"brief-structuring"}`},
			{contracts.ActionAgentRestoreSkill, `{"name":"brief-structuring"}`},
			{contracts.ActionAgentSaveSubagent, `{"name":"mod-sub","description":"Mod sub","tools":[],"skills":[],"enabled":true,"body":"Inspect"}`},
			{contracts.ActionAgentDeleteSubagent, `{"name":"mod-sub"}`},
			{contracts.ActionAgentDeleteSubagent, `{"name":"workspace-reviewer"}`},
			{contracts.ActionAgentRestoreSubagent, `{"name":"workspace-reviewer"}`},
		} {
			if _, err := mustCall(t, reg, tc.action, tc.payload); err != nil {
				t.Fatalf("%s failed when unlocked: %v", tc.action, err)
			}
		}
	})
}

func TestAgentModule_ResolveApprovalForwardsErrors(t *testing.T) {
	svc, _ := newTestAgentService(t)
	reg := realtime.NewRegistry(contracts.ActionNames())
	modules.Agent{Service: svc}.Register(reg)

	// Empty approval ID fails payload validation before reaching service.
	_, err := mustCall(t, reg, contracts.ActionAgentResolveApproval, `{"id":"   ","approve":true}`)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeValidation {
		t.Fatalf("expected ERR_VALIDATION for blank approval id, got %v", err)
	}

	// Unknown approval ID forwards service's *apperr.Error (CodeNotFound).
	_, err = mustCall(t, reg, contracts.ActionAgentResolveApproval, `{"id":"missing-approval","approve":true}`)
	ae = nil
	if !errors.As(err, &ae) || ae.Code != apperr.CodeNotFound {
		t.Fatalf("expected ERR_NOT_FOUND forwarded from ResolveApproval, got %v", err)
	}
	if ae.Message == "" {
		t.Fatal("expected non-empty error message from ResolveApproval")
	}

	// Rejecting a missing approval ID also forwards CodeNotFound cleanly.
	_, err = mustCall(t, reg, contracts.ActionAgentResolveApproval, `{"id":"missing-approval","approve":false}`)
	ae = nil
	if !errors.As(err, &ae) || ae.Code != apperr.CodeNotFound {
		t.Fatalf("expected ERR_NOT_FOUND on reject of missing approval, got %v", err)
	}
}
