package test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
)

const (
	errHostAdminLocked      = "host path and desktop folder actions are disabled in server mode"
	errTrustedFoldersLocked = "trusted folders can only be changed in the desktop app on this machine"
	errCustomizationsLocked = "agent skills and subagents cannot be modified remotely in server mode"
)

func startServerCore(t *testing.T, allowHostAdmin bool) *harness {
	t.Helper()
	return startCore(t, func(c *config.Config) {
		c.Mode = config.ModeServer
		c.Server.AllowHostAdmin = allowHostAdmin
		c.Providers.AutoCheck = false
		c.Providers.CheckOnStartup = false
	})
}

func assertUnavailableFrame(t *testing.T, action string, resp frame, wantMsg string) {
	t.Helper()
	if resp.Success {
		t.Fatalf("%s: expected failure, got success", action)
	}
	if resp.Error == nil {
		t.Fatalf("%s: expected error body, got nil", action)
	}
	if resp.Error.Code != "ERR_UNAVAILABLE" {
		t.Fatalf("%s: expected ERR_UNAVAILABLE, got %+v", action, resp.Error)
	}
	if resp.Error.Message != wantMsg {
		t.Fatalf("%s: expected message %q, got %q", action, wantMsg, resp.Error.Message)
	}
}

func assertProvidersAccessible(t *testing.T, ws *wsClient) {
	t.Helper()

	listRes, _ := ws.call(contracts.ActionProvidersList, contracts.Empty{})
	if !listRes.Success {
		t.Fatalf("providers:list failed: %+v", listRes.Error)
	}
	var list contracts.ProvidersListResult
	if err := json.Unmarshal(listRes.Data, &list); err != nil {
		t.Fatalf("unmarshal providers:list: %v", err)
	}
	if len(list.Builtins) == 0 {
		t.Fatal("expected at least one built-in provider")
	}

	defRes, _ := ws.call(contracts.ActionProvidersDefaults, contracts.Empty{})
	if !defRes.Success {
		t.Fatalf("providers:defaults failed: %+v", defRes.Error)
	}

	updRes, _ := ws.call(contracts.ActionProvidersUpdateSettings, contracts.UpdateSettingsPayload{
		OwnerID:  list.Builtins[0].ID,
		Settings: map[string]string{domainprovider.SettingRequestGapMs: "100"},
	})
	if !updRes.Success {
		t.Fatalf("providers:update_settings failed: %+v", updRes.Error)
	}
	var settings contracts.ProviderSettingsResult
	if err := json.Unmarshal(updRes.Data, &settings); err != nil {
		t.Fatalf("unmarshal providers:update_settings: %v", err)
	}
	if settings.Settings[domainprovider.SettingRequestGapMs] != 100 {
		t.Fatalf("expected %s=100, got %+v", domainprovider.SettingRequestGapMs, settings.Settings)
	}
}

func TestServerModeAuthz_DefaultLocked(t *testing.T) {
	h := startServerCore(t, false)

	// Unauthenticated WebSocket connection must be rejected in server mode with AuthToken set.
	if _, _, err := websocket.Dial(context.Background(), h.core.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion},
	}); err == nil {
		t.Fatal("expected unauthenticated WebSocket connection in server mode to be rejected")
	}

	ws := h.dial()

	// Host-admin actions are rejected with ERR_UNAVAILABLE.
	resp, _ := ws.call(contracts.ActionSystemOpenFolder, contracts.OpenFolderPayload{Target: contracts.FolderLogs})
	assertUnavailableFrame(t, contracts.ActionSystemOpenFolder, resp, errHostAdminLocked)

	resp, _ = ws.call(contracts.ActionBinariesSetPath, contracts.BinarySetPathPayload{
		ID:         "ffmpeg",
		CustomPath: `C:\custom\ffmpeg.exe`,
	})
	assertUnavailableFrame(t, contracts.ActionBinariesSetPath, resp, errHostAdminLocked)

	resp, _ = ws.call(contracts.ActionAgentSetPath, contracts.AgentSetPathPayload{
		ID:         "antigravity",
		CustomPath: `C:\custom\agy.exe`,
	})
	assertUnavailableFrame(t, contracts.ActionAgentSetPath, resp, errHostAdminLocked)

	// All 6 agent customization mutators are rejected with ERR_UNAVAILABLE.
	customizationMutators := []struct {
		action  string
		payload any
	}{
		{
			action: contracts.ActionAgentSaveSkill,
			payload: contracts.AgentSaveSkillPayload{
				Name:          "remote-skill",
				Description:   "Should be blocked",
				UserInvocable: true,
				Enabled:       true,
				Body:          "Body",
			},
		},
		{
			action:  contracts.ActionAgentDeleteSkill,
			payload: contracts.AgentCustomizationIDPayload{Name: "brief-structuring"},
		},
		{
			action:  contracts.ActionAgentRestoreSkill,
			payload: contracts.AgentCustomizationIDPayload{Name: "brief-structuring"},
		},
		{
			action: contracts.ActionAgentSaveSubagent,
			payload: contracts.AgentSaveSubagentPayload{
				Name:        "remote-sub",
				Description: "Should be blocked",
				Tools:       []string{},
				Skills:      []string{},
				Enabled:     true,
				Body:        "Body",
			},
		},
		{
			action:  contracts.ActionAgentDeleteSubagent,
			payload: contracts.AgentCustomizationIDPayload{Name: "workspace-reviewer"},
		},
		{
			action:  contracts.ActionAgentRestoreSubagent,
			payload: contracts.AgentCustomizationIDPayload{Name: "workspace-reviewer"},
		},
	}
	for _, tc := range customizationMutators {
		resp, _ := ws.call(tc.action, tc.payload)
		assertUnavailableFrame(t, tc.action, resp, errCustomizationsLocked)
	}

	// Trusted-folder mutators are always locked in server mode.
	trustedDir := t.TempDir()
	resp, _ = ws.call(contracts.ActionAgentAddAllowedDir, contracts.AgentAllowedDirPayload{Dir: trustedDir})
	assertUnavailableFrame(t, contracts.ActionAgentAddAllowedDir, resp, errTrustedFoldersLocked)

	resp, _ = ws.call(contracts.ActionAgentRemoveAllowedDir, contracts.AgentAllowedDirPayload{Dir: trustedDir})
	assertUnavailableFrame(t, contracts.ActionAgentRemoveAllowedDir, resp, errTrustedFoldersLocked)

	// Allowed read actions and providers:* with valid token still work.
	infoRes, _ := ws.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoRes.Success {
		t.Fatalf("system:info failed: %+v", infoRes.Error)
	}
	var sysInfo contracts.SystemInfo
	if err := json.Unmarshal(infoRes.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	if sysInfo.Mode != config.ModeServer {
		t.Fatalf("expected mode %q, got %q", config.ModeServer, sysInfo.Mode)
	}

	for _, readAction := range []string{
		contracts.ActionBinariesList,
		contracts.ActionAgentListAllowedDirs,
		contracts.ActionAgentListCustomizations,
	} {
		r, _ := ws.call(readAction, contracts.Empty{})
		if !r.Success {
			t.Fatalf("%s failed in server mode: %+v", readAction, r.Error)
		}
	}

	assertProvidersAccessible(t, ws)
}

func TestServerModeAuthz_AllowHostAdminEnabled(t *testing.T) {
	h := startServerCore(t, true)
	ws := h.dial()

	// Host-admin mutators are no longer blocked by the host-admin guard.
	openRes, _ := ws.call(contracts.ActionSystemOpenFolder, contracts.OpenFolderPayload{Target: contracts.FolderLogs})
	if !openRes.Success {
		t.Fatalf("system:open_folder failed with AllowHostAdmin=true: %+v", openRes.Error)
	}

	binRes, _ := ws.call(contracts.ActionBinariesSetPath, contracts.BinarySetPathPayload{
		ID:         "ffmpeg",
		CustomPath: `C:\custom\ffmpeg.exe`,
	})
	if !binRes.Success {
		t.Fatalf("binaries:set_path failed with AllowHostAdmin=true: %+v", binRes.Error)
	}
	var binInfo contracts.BinaryInfo
	if err := json.Unmarshal(binRes.Data, &binInfo); err != nil {
		t.Fatalf("unmarshal binaries:set_path: %v", err)
	}
	if binInfo.CustomPath != `C:\custom\ffmpeg.exe` {
		t.Fatalf("expected custom_path C:\\custom\\ffmpeg.exe, got %q", binInfo.CustomPath)
	}

	agentRes, _ := ws.call(contracts.ActionAgentSetPath, contracts.AgentSetPathPayload{
		ID:         "antigravity",
		CustomPath: `C:\custom\agy.exe`,
	})
	if !agentRes.Success {
		t.Fatalf("agent:set_path failed with AllowHostAdmin=true: %+v", agentRes.Error)
	}
	var agentInfo contracts.AgentProviderInfo
	if err := json.Unmarshal(agentRes.Data, &agentInfo); err != nil {
		t.Fatalf("unmarshal agent:set_path: %v", err)
	}
	if agentInfo.CustomPath != `C:\custom\agy.exe` {
		t.Fatalf("expected custom_path C:\\custom\\agy.exe, got %q", agentInfo.CustomPath)
	}

	// All 6 agent customization mutators are unlocked when AllowHostAdmin=true.
	unlockedCustomizationSteps := []struct {
		action  string
		payload any
	}{
		{
			action: contracts.ActionAgentSaveSkill,
			payload: contracts.AgentSaveSkillPayload{
				Name:          "server-admin-skill",
				Description:   "Allowed with AllowHostAdmin=true",
				UserInvocable: true,
				Enabled:       true,
				Body:          "Follow step 1.",
			},
		},
		{
			action:  contracts.ActionAgentDeleteSkill,
			payload: contracts.AgentCustomizationIDPayload{Name: "server-admin-skill"},
		},
		{
			action:  contracts.ActionAgentDeleteSkill,
			payload: contracts.AgentCustomizationIDPayload{Name: "brief-structuring"},
		},
		{
			action:  contracts.ActionAgentRestoreSkill,
			payload: contracts.AgentCustomizationIDPayload{Name: "brief-structuring"},
		},
		{
			action: contracts.ActionAgentSaveSubagent,
			payload: contracts.AgentSaveSubagentPayload{
				Name:        "server-admin-sub",
				Description: "Allowed with AllowHostAdmin=true",
				Tools:       []string{},
				Skills:      []string{},
				Enabled:     true,
				Body:        "Inspect safely.",
			},
		},
		{
			action:  contracts.ActionAgentDeleteSubagent,
			payload: contracts.AgentCustomizationIDPayload{Name: "server-admin-sub"},
		},
		{
			action:  contracts.ActionAgentDeleteSubagent,
			payload: contracts.AgentCustomizationIDPayload{Name: "workspace-reviewer"},
		},
		{
			action:  contracts.ActionAgentRestoreSubagent,
			payload: contracts.AgentCustomizationIDPayload{Name: "workspace-reviewer"},
		},
	}
	for _, step := range unlockedCustomizationSteps {
		resp, _ := ws.call(step.action, step.payload)
		if !resp.Success {
			t.Fatalf("%s failed with AllowHostAdmin=true: %+v", step.action, resp.Error)
		}
	}

	// Trusted-folder mutators MUST remain locked even when AllowHostAdmin=true.
	trustedDir := t.TempDir()
	resp, _ := ws.call(contracts.ActionAgentAddAllowedDir, contracts.AgentAllowedDirPayload{Dir: trustedDir})
	assertUnavailableFrame(t, contracts.ActionAgentAddAllowedDir, resp, errTrustedFoldersLocked)

	resp, _ = ws.call(contracts.ActionAgentRemoveAllowedDir, contracts.AgentAllowedDirPayload{Dir: trustedDir})
	assertUnavailableFrame(t, contracts.ActionAgentRemoveAllowedDir, resp, errTrustedFoldersLocked)

	assertProvidersAccessible(t, ws)
}
