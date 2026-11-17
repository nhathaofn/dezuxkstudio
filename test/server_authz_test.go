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
	errHostAdminLocked         = "host path and desktop folder actions are disabled in server mode"
	errTrustedFoldersLocked    = "trusted folders can only be changed in the desktop app on this machine"
	errCustomizationsLocked    = "agent skills and subagents cannot be modified remotely in server mode"
	errWorkspaceSettingsLocked = "trusted folders and workspace settings can only be changed in the desktop app on this machine"
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

func assertValidationFrame(t *testing.T, action string, resp frame) {
	t.Helper()
	if resp.Success {
		t.Fatalf("%s: expected validation failure, got success", action)
	}
	if resp.Error == nil {
		t.Fatalf("%s: expected error body, got nil", action)
	}
	if resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("%s: expected ERR_VALIDATION, got %+v", action, resp.Error)
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

	// Workspace settings mutator is locked in server mode.
	resp, _ = ws.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": `C:\custom\workspace`,
	})
	assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)

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

	// Workspace settings mutator MUST remain locked even when AllowHostAdmin=true.
	resp, _ = ws.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": `C:\custom\workspace`,
	})
	assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)

	assertProvidersAccessible(t, ws)
}

// TestServerModeAuthz_PreferencesLockdown verifies Tier 1 server mode lockdown:
// 1. workspace_root, onboarding_completed, agent_skip_trusted_approval are rejected with ERR_UNAVAILABLE.
// 2. Lockdown applies equally under both allowHostAdmin = false and allowHostAdmin = true.
// 3. Modifying allowed fields (theme and language) succeeds and is persisted.
func TestServerModeAuthz_PreferencesLockdown(t *testing.T) {
	for _, allowHostAdmin := range []bool{false, true} {
		name := "DefaultLocked"
		if allowHostAdmin {
			name = "AllowHostAdminEnabled"
		}

		t.Run(name, func(t *testing.T) {
			h := startServerCore(t, allowHostAdmin)
			ws := h.dial()

			// 1. Forbidden Field: workspace_root
			resp, events := ws.call(contracts.ActionPreferencesUpdate, map[string]any{
				"workspace_root": `C:\hacked\workspace`,
			})
			assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("unexpected EventPreferencesUpdated broadcast leaked on forbidden workspace_root update: %+v", ev)
				}
			}

			// 2. Forbidden Field: onboarding_completed
			resp, events = ws.call(contracts.ActionPreferencesUpdate, map[string]any{
				"onboarding_completed": true,
			})
			assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("unexpected EventPreferencesUpdated broadcast leaked on forbidden onboarding_completed update: %+v", ev)
				}
			}

			// 3. Forbidden Field: agent_skip_trusted_approval
			resp, events = ws.call(contracts.ActionPreferencesUpdate, map[string]any{
				"agent_skip_trusted_approval": true,
			})
			assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("unexpected EventPreferencesUpdated broadcast leaked on forbidden agent_skip_trusted_approval update: %+v", ev)
				}
			}

			// 4. Allowed Fields: theme and language MUST succeed
			resp, _ = ws.call(contracts.ActionPreferencesUpdate, map[string]any{
				"theme":    "dark",
				"language": "vi",
			})
			if !resp.Success {
				t.Fatalf("preferences:update failed for allowed fields: %+v", resp.Error)
			}

			// Verify updated preferences reflect allowed changes
			getResp, _ := ws.call(contracts.ActionPreferencesGet, contracts.Empty{})
			if !getResp.Success {
				t.Fatalf("preferences:get failed: %+v", getResp.Error)
			}
			var prefs wirePreferences
			if err := json.Unmarshal(getResp.Data, &prefs); err != nil {
				t.Fatalf("unmarshal preferences: %v", err)
			}
			if prefs.Theme != "dark" || prefs.Language != "vi" {
				t.Fatalf("expected theme=dark, language=vi; got theme=%s, language=%s", prefs.Theme, prefs.Language)
			}
			if prefs.WorkspaceRoot != "" {
				t.Fatalf("expected workspace_root to remain empty, got %q", prefs.WorkspaceRoot)
			}
			if prefs.OnboardingCompleted {
				t.Fatalf("expected onboarding_completed to remain false, got %v", prefs.OnboardingCompleted)
			}
			if prefs.AgentSkipTrustedApproval {
				t.Fatalf("expected agent_skip_trusted_approval to remain false, got %v", prefs.AgentSkipTrustedApproval)
			}
		})
	}
}

// TestServerModeAuthz_PreferencesBypassAndAtomicity verifies Tier 2 adversarial boundaries (B10):
// 1. Hybrid payloads combining allowed and forbidden fields are completely rejected.
// 2. Empty reset bypass, whitespace bypass, key casing bypass, and explicit false toggle bypass attempts are rejected.
// 3. Multiple forbidden fields combinations are rejected.
// 4. Atomicity check: rejected updates do NOT cause partial mutations or emit broadcast events.
func TestServerModeAuthz_PreferencesBypassAndAtomicity(t *testing.T) {
	h := startServerCore(t, false)
	ws := h.dial()

	// Capture initial baseline preferences
	initResp, _ := ws.call(contracts.ActionPreferencesGet, contracts.Empty{})
	if !initResp.Success {
		t.Fatalf("initial preferences:get failed: %+v", initResp.Error)
	}
	var baseline wirePreferences
	if err := json.Unmarshal(initResp.Data, &baseline); err != nil {
		t.Fatalf("unmarshal baseline preferences: %v", err)
	}

	boundaryTestCases := []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "Hybrid Piggyback: allowed theme + forbidden workspace_root",
			payload: map[string]any{
				"theme":          "dark",
				"workspace_root": `D:\adversarial\path`,
			},
		},
		{
			name: "Hybrid Piggyback: allowed language + forbidden agent_skip_trusted_approval",
			payload: map[string]any{
				"language":                    "vi",
				"agent_skip_trusted_approval": true,
			},
		},
		{
			name: "Hybrid Piggyback: allowed theme + forbidden onboarding_completed",
			payload: map[string]any{
				"theme":                "dark",
				"onboarding_completed": true,
			},
		},
		{
			name: "Empty Value Reset Bypass: workspace_root empty string",
			payload: map[string]any{
				"workspace_root": "",
			},
		},
		{
			name: "Whitespace-padded valid path bypass: workspace_root padded",
			payload: map[string]any{
				"workspace_root": "   C:\\adversarial\\path   ",
			},
		},
		{
			name: "Casing UPPERCASE: WORKSPACE_ROOT",
			payload: map[string]any{
				"WORKSPACE_ROOT": `C:\adversarial\path`,
			},
		},
		{
			name: "Casing PascalCase: Workspace_Root",
			payload: map[string]any{
				"Workspace_Root": `C:\adversarial\path`,
			},
		},
		{
			name: "Explicit False Toggle Bypass: agent_skip_trusted_approval false",
			payload: map[string]any{
				"agent_skip_trusted_approval": false,
			},
		},
		{
			name: "Explicit False Toggle Bypass: onboarding_completed false",
			payload: map[string]any{
				"onboarding_completed": false,
			},
		},
		{
			name: "Multiple Forbidden Fields: all 3 forbidden fields together",
			payload: map[string]any{
				"workspace_root":              `C:\custom\root`,
				"onboarding_completed":        true,
				"agent_skip_trusted_approval": true,
			},
		},
		{
			name: "All Fields Mixed: theme + language + all 3 forbidden fields",
			payload: map[string]any{
				"theme":                       "dark",
				"language":                    "vi",
				"workspace_root":              `C:\custom\root`,
				"onboarding_completed":        true,
				"agent_skip_trusted_approval": true,
			},
		},
	}

	for _, tc := range boundaryTestCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, events := ws.call(contracts.ActionPreferencesUpdate, tc.payload)

			// 1. Must be rejected with ERR_UNAVAILABLE
			assertUnavailableFrame(t, contracts.ActionPreferencesUpdate, resp, errWorkspaceSettingsLocked)

			// 2. Must NOT emit EventPreferencesUpdated broadcast event
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("%s: unexpected EventPreferencesUpdated broadcast on rejected update: %+v", tc.name, ev)
				}
			}

			// 3. Atomicity Invariant Check: Verify NO partial mutation occurred
			currResp, _ := ws.call(contracts.ActionPreferencesGet, contracts.Empty{})
			var current wirePreferences
			if err := json.Unmarshal(currResp.Data, &current); err != nil {
				t.Fatalf("%s: unmarshal current preferences: %v", tc.name, err)
			}

			if current.Theme != baseline.Theme {
				t.Fatalf("%s: partial state mutation detected! Theme mutated from %q to %q",
					tc.name, baseline.Theme, current.Theme)
			}
			if current.Language != baseline.Language {
				t.Fatalf("%s: partial state mutation detected! Language mutated from %q to %q",
					tc.name, baseline.Language, current.Language)
			}
			if current.WorkspaceRoot != baseline.WorkspaceRoot {
				t.Fatalf("%s: partial state mutation detected! WorkspaceRoot mutated from %q to %q",
					tc.name, baseline.WorkspaceRoot, current.WorkspaceRoot)
			}
			if current.OnboardingCompleted != baseline.OnboardingCompleted {
				t.Fatalf("%s: partial state mutation detected! OnboardingCompleted mutated from %v to %v",
					tc.name, baseline.OnboardingCompleted, current.OnboardingCompleted)
			}
			if current.AgentSkipTrustedApproval != baseline.AgentSkipTrustedApproval {
				t.Fatalf("%s: partial state mutation detected! AgentSkipTrustedApproval mutated from %v to %v",
					tc.name, baseline.AgentSkipTrustedApproval, current.AgentSkipTrustedApproval)
			}
		})
	}
}

// TestServerModeAuthz_PreferencesProtocolRejection verifies that unknown fields,
// camelCase keys, or malformed types are rejected at the protocol validation stage (ERR_VALIDATION)
// rather than authorization stage (ERR_UNAVAILABLE), and leave server state completely unmutated.
func TestServerModeAuthz_PreferencesProtocolRejection(t *testing.T) {
	h := startServerCore(t, false)
	ws := h.dial()

	// Capture initial baseline preferences
	initResp, _ := ws.call(contracts.ActionPreferencesGet, contracts.Empty{})
	if !initResp.Success {
		t.Fatalf("initial preferences:get failed: %+v", initResp.Error)
	}
	var baseline wirePreferences
	if err := json.Unmarshal(initResp.Data, &baseline); err != nil {
		t.Fatalf("unmarshal baseline preferences: %v", err)
	}

	protocolCases := []struct {
		name    string
		payload map[string]any
	}{
		{
			name: "Unknown extra field: unrecognized",
			payload: map[string]any{
				"unrecognized": 123,
			},
		},
		{
			name: "Unknown camelCase: workspaceRoot",
			payload: map[string]any{
				"workspaceRoot": `C:\custom\root`,
			},
		},
		{
			name: "Malformed type: workspace_root as array",
			payload: map[string]any{
				"workspace_root": []string{`C:\custom\root`},
			},
		},
		{
			name: "Malformed type: onboarding_completed as string",
			payload: map[string]any{
				"onboarding_completed": "true",
			},
		},
		{
			name: "Allowed field mixed with unknown field: theme + admin",
			payload: map[string]any{
				"theme": "dark",
				"admin": true,
			},
		},
		{
			name: "Invalid path syntax: whitespace-only workspace_root",
			payload: map[string]any{
				"workspace_root": "   \t   ",
			},
		},
	}

	for _, tc := range protocolCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, events := ws.call(contracts.ActionPreferencesUpdate, tc.payload)

			// 1. Must be rejected with ERR_VALIDATION
			assertValidationFrame(t, contracts.ActionPreferencesUpdate, resp)

			// 2. Must NOT emit EventPreferencesUpdated broadcast event
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("%s: unexpected EventPreferencesUpdated broadcast on rejected protocol payload: %+v", tc.name, ev)
				}
			}

			// 3. Atomicity Invariant Check: Verify NO partial mutation occurred
			currResp, _ := ws.call(contracts.ActionPreferencesGet, contracts.Empty{})
			var current wirePreferences
			if err := json.Unmarshal(currResp.Data, &current); err != nil {
				t.Fatalf("%s: unmarshal current preferences: %v", tc.name, err)
			}

			if current != baseline {
				t.Fatalf("%s: state mutation detected on protocol rejection! baseline=%+v, current=%+v",
					tc.name, baseline, current)
			}
		})
	}
}
