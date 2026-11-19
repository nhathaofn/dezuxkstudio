package test

import (
	"context"
	"encoding/json"
	"image/color"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"app/internal/config"
	"app/internal/contracts"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
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

func assertProvidersReadOnly(t *testing.T, ws *wsClient) contracts.ProvidersListResult {
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
	return list
}

func assertProvidersAccessible(t *testing.T, ws *wsClient) {
	t.Helper()
	list := assertProvidersReadOnly(t, ws)

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
	if sysInfo.Paths != (contracts.PathsInfo{}) {
		t.Fatalf("expected system:info host paths hidden in locked server mode, got %+v", sysInfo.Paths)
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

	list := assertProvidersReadOnly(t, ws)

	lockedHostAdmin := []struct {
		action  string
		payload any
	}{
		{contracts.ActionBinariesDownload, contracts.BinaryIDPayload{ID: "ffmpeg"}},
		{contracts.ActionDiagnosticsExport, contracts.Empty{}},
		{contracts.ActionSystemCheckUpdate, contracts.Empty{}},
		{contracts.ActionStudioPricingUpsert, contracts.StudioPricingUpsertPayload{
			ProviderID: "openai", ModelID: "gpt-4o", Capability: "text", Unit: "1k_tokens", UnitPriceUSD: 0.01,
		}},
		{contracts.ActionProvidersCreateCredential, contracts.CreateCredentialPayload{ProviderID: list.Builtins[0].ID}},
		{contracts.ActionProvidersUpdateCredential, contracts.UpdateCredentialPayload{ID: "c1"}},
		{contracts.ActionProvidersDeleteCredential, contracts.ProviderIDPayload{ID: "c1"}},
		{contracts.ActionProvidersActivate, contracts.ProviderIDPayload{ID: "c1"}},
		{contracts.ActionProvidersTestCredential, contracts.ProviderIDPayload{ID: "c1"}},
		{contracts.ActionProvidersCreateCustom, contracts.CustomProviderInput{Name: "x"}},
		{contracts.ActionProvidersUpdateCustom, contracts.UpdateCustomPayload{ID: "x"}},
		{contracts.ActionProvidersDeleteCustom, contracts.ProviderIDPayload{ID: "x"}},
		{contracts.ActionProvidersTestCustom, contracts.ProviderIDPayload{ID: "x"}},
		{contracts.ActionProvidersDiscoverModels, contracts.DiscoverModelsPayload{CustomID: "x"}},
		{contracts.ActionProvidersDiscoverBuiltin, contracts.ProviderIDPayload{ID: list.Builtins[0].ID}},
		{contracts.ActionProvidersSetModels, contracts.SetModelsPayload{OwnerID: list.Builtins[0].ID}},
		{contracts.ActionProvidersUpdateSettings, contracts.UpdateSettingsPayload{OwnerID: list.Builtins[0].ID, Settings: map[string]string{"request_gap_ms": "100"}}},
		{contracts.ActionProvidersSetDefault, contracts.SetDefaultPayload{Purpose: "text", Model: "openai/gpt-4o"}},
		{contracts.ActionAgentLogin, contracts.AgentLoginPayload{ID: "claude"}},
		{contracts.ActionAgentLoginCode, contracts.AgentLoginCodePayload{ID: "codex", Code: "123"}},
		{contracts.ActionAgentLogout, contracts.AgentProviderIDPayload{ID: "claude"}},
		{contracts.ActionAgentConnect, contracts.AgentProviderIDPayload{ID: "claude"}},
		{contracts.ActionAgentSetModel, contracts.AgentSetModelPayload{ID: "claude", Model: "sonnet"}},
		{contracts.ActionAgentLinkProvider, contracts.AgentLinkProviderPayload{AgentProviderID: "claude", ProviderID: "anthropic", CredentialID: "c1"}},
		{contracts.ActionAgentUnlinkProvider, contracts.AgentProviderIDPayload{ID: "claude"}},
		{contracts.ActionAgentReloadCustomizations, contracts.Empty{}},
	}
	for _, tc := range lockedHostAdmin {
		r, _ := ws.call(tc.action, tc.payload)
		assertUnavailableFrame(t, tc.action, r, errHostAdminLocked)
	}
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

func TestServerModeAuthz_StudioOpenExportFolderLockdown(t *testing.T) {
	t.Run("DefaultLocked", func(t *testing.T) {
		h := startServerCore(t, false)
		ws := h.dial()

		resp, _ := ws.call(contracts.ActionStudioOpenExportFolder, contracts.StudioOpenExportFolderPayload{
			EpisodeID: "ep-any",
		})
		assertUnavailableFrame(t, contracts.ActionStudioOpenExportFolder, resp, errHostAdminLocked)
	})

	t.Run("AllowHostAdminEnabled", func(t *testing.T) {
		h := startServerCore(t, true)
		ws := h.dial()

		projResp, _ := ws.call(contracts.ActionStudioProjectCreate, contracts.StudioProjectCreatePayload{
			Title: "Server Admin Film",
		})
		if !projResp.Success {
			t.Fatalf("studio:project_create failed: %+v", projResp.Error)
		}
		var detail contracts.StudioProjectDetailResult
		if err := json.Unmarshal(projResp.Data, &detail); err != nil {
			t.Fatalf("unmarshal project detail: %v", err)
		}

		openResp, _ := ws.call(contracts.ActionStudioOpenExportFolder, contracts.StudioOpenExportFolderPayload{
			EpisodeID: detail.ActiveEpisode.Episode.ID,
		})
		if !openResp.Success {
			t.Fatalf("studio:open_export_folder failed with AllowHostAdmin=true: %+v", openResp.Error)
		}
	})
}

func TestServerModeAuthz_StudioAndMediaFileGuards(t *testing.T) {
	h := startServerCore(t, false)
	ws := h.dial()

	infoResp, _ := ws.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	downloadByPath := func(rawPath string) int {
		t.Helper()
		q := url.Values{}
		q.Set("path", rawPath)
		req, err := http.NewRequest(http.MethodGet, h.httpURL("/api/media/download?"+q.Encode()), nil)
		if err != nil {
			t.Fatalf("new download request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+h.token)
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("do download request: %v", err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	// 1. media:register_input on untrusted file outside workspace MUST be rejected,
	//    AND subsequent GET /api/media/download?path=... MUST still return 403 (no allowedRoots poisoning!).
	outsideDir := createE2EWorkspaceRoot(t, "ServerUntrustedOutside")
	outsidePNG := filepath.Join(outsideDir, "secret_capture.png")
	createSizedPNG(t, outsidePNG, 4, 4, color.RGBA{R: 200, G: 20, B: 20, A: 255})

	regOutsideResp, _ := ws.call(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   outsidePNG,
		Kind:       "image",
		SourceNode: "untrusted_probe",
	})
	if regOutsideResp.Success || regOutsideResp.Error == nil || regOutsideResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected media:register_input outside workspace to fail with ERR_UNAUTHORIZED, got %+v", regOutsideResp)
	}
	if strings.Contains(regOutsideResp.Error.Message, outsideDir) {
		t.Fatalf("error message leaked server host path %q: %q", outsideDir, regOutsideResp.Error.Message)
	}
	if code := downloadByPath(outsidePNG); code != http.StatusForbidden {
		t.Fatalf("expected GET /api/media/download?path=<outside> to return 403 after rejected register_input, got %d", code)
	}

	// Positive control: registering & downloading a legitimate image inside MediaDir succeeds in --mode server.
	tree := h.core.WorkspaceTree()
	trustedMediaPNG := filepath.Join(tree.MediaDir, "images", "trusted_frame.png")
	createSizedPNG(t, trustedMediaPNG, 4, 4, color.RGBA{R: 20, G: 180, B: 90, A: 255})
	regTrustedResp, _ := ws.call(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   trustedMediaPNG,
		Kind:       "image",
		SourceNode: "trusted_probe",
	})
	if !regTrustedResp.Success {
		t.Fatalf("expected media:register_input inside MediaDir to succeed, got %+v", regTrustedResp.Error)
	}
	if code := downloadByPath(trustedMediaPNG); code != http.StatusOK {
		t.Fatalf("expected GET /api/media/download?path=<MediaDir/images/...> to return 200, got %d", code)
	}

	// 2. GET /api/media/download?path=<workspace>/.env and internal app.db MUST return 403 Forbidden.
	wsEnvPath := filepath.Join(tree.WorkspaceDir, ".env")
	if err := os.WriteFile(wsEnvPath, []byte("OPENAI_API_KEY=sk-secret\n"), 0o600); err != nil {
		t.Fatalf("write workspace .env: %v", err)
	}
	if code := downloadByPath(wsEnvPath); code != http.StatusForbidden {
		t.Fatalf("security violation: GET /api/media/download?path=<workspace>/.env returned %d (expected 403)", code)
	}
	appDBPath := filepath.Join(h.paths.Data, "app.db")
	if code := downloadByPath(appDBPath); code != http.StatusForbidden {
		t.Fatalf("security violation: GET /api/media/download?path=<DataDir>/app.db returned %d (expected 403)", code)
	}

	// 3. Studio FileGuard in --mode server: create trusted project, reject path escapes & forbidden files,
	//    and strip smuggled outside-workspace paths on studio:sync_workspace.
	createProjResp, _ := ws.call(contracts.ActionStudioProjectCreate, contracts.StudioProjectCreatePayload{
		Title: "Guarded Server Project",
	})
	if !createProjResp.Success {
		t.Fatalf("studio:project_create in server mode failed: %+v", createProjResp.Error)
	}
	var projDetail contracts.StudioProjectDetailResult
	if err := json.Unmarshal(createProjResp.Data, &projDetail); err != nil {
		t.Fatalf("unmarshal project detail: %v", err)
	}
	projID := projDetail.Project.ID
	epID := projDetail.ActiveEpisode.Episode.ID

	// Reject untrusted PrimaryPath outside workspace on studio:asset_upsert.
	badAssetResp, _ := ws.call(contracts.ActionStudioAssetUpsert, contracts.StudioAssetUpsertPayload{
		ProjectID:   projID,
		Kind:        domainstudio.AssetCharacter,
		Name:        "Intruder",
		PrimaryPath: outsidePNG,
	})
	if badAssetResp.Success || badAssetResp.Error == nil || badAssetResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected studio:asset_upsert with outside path to fail with ERR_UNAUTHORIZED, got %+v", badAssetResp)
	}

	// Reject forbidden .env inside workspace on studio:asset_upsert.
	envAssetResp, _ := ws.call(contracts.ActionStudioAssetUpsert, contracts.StudioAssetUpsertPayload{
		ProjectID:   projID,
		Kind:        domainstudio.AssetProp,
		Name:        "EnvLeak",
		PrimaryPath: wsEnvPath,
	})
	if envAssetResp.Success || envAssetResp.Error == nil || envAssetResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected studio:asset_upsert with .env path to fail with ERR_UNAUTHORIZED, got %+v", envAssetResp)
	}

	// Reject path traversal outside workspace on studio:shot_upsert.
	escapedShotPath := filepath.Join(projDetail.Project.RootDir, "..", "..", "..", "app.db")
	badShotResp, _ := ws.call(contracts.ActionStudioShotUpsert, contracts.StudioShotUpsertPayload{
		EpisodeID:    epID,
		Title:        "Escaped Shot",
		KeyframePath: &escapedShotPath,
	})
	if badShotResp.Success || badShotResp.Error == nil || badShotResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected studio:shot_upsert with traversal path to fail with ERR_UNAUTHORIZED, got %+v", badShotResp)
	}

	// Reject outside-workspace ImagePath on studio:grid_split.
	badGridResp, _ := ws.call(contracts.ActionStudioGridSplit, contracts.StudioGridSplitPayload{
		ProjectID: projID,
		EpisodeID: epID,
		ImagePath: outsidePNG,
		Rows:      2,
		Cols:      2,
	})
	if badGridResp.Success || badGridResp.Error == nil || badGridResp.Error.Code != "ERR_UNAUTHORIZED" {
		t.Fatalf("expected studio:grid_split with outside image to fail with ERR_UNAUTHORIZED, got %+v", badGridResp)
	}

	// Smuggle untrusted root_dir + auto_approve_batches in project.json and outside-workspace/.env paths in asset_bible.json & shots.json.
	origRootDir := projDetail.Project.RootDir
	projJSONPath := filepath.Join(origRootDir, "project.json")
	poisonedProj, _ := json.Marshal(map[string]any{
		"title":                "Guarded Server Project",
		"root_dir":             outsideDir,
		"auto_approve_batches": true,
	})
	if err := os.WriteFile(projJSONPath, poisonedProj, 0o600); err != nil {
		t.Fatalf("write poisoned project.json: %v", err)
	}

	biblePath := filepath.Join(origRootDir, "assets", "asset_bible.json")
	poisonedBible, _ := json.Marshal(map[string]any{
		"assets": []map[string]any{
			{
				"kind":         "character",
				"name":         "SmuggledHero",
				"code_tag":     "@SmuggledHero",
				"primary_path": outsidePNG,
			},
		},
	})
	if err := os.WriteFile(biblePath, poisonedBible, 0o600); err != nil {
		t.Fatalf("write poisoned asset_bible.json: %v", err)
	}

	shotsPath := filepath.Join(origRootDir, "episodes", "ep01", "storyboard", "shots.json")
	poisonedShots, _ := json.Marshal(map[string]any{
		"shots": []map[string]any{
			{
				"seq":           1,
				"scene_code":    "S01",
				"title":         "SmuggledShot",
				"shot_mode":     "single",
				"duration_sec":  4,
				"keyframe_path": outsidePNG,
				"video_path":    wsEnvPath,
			},
		},
	})
	if err := os.WriteFile(shotsPath, poisonedShots, 0o600); err != nil {
		t.Fatalf("write poisoned shots.json: %v", err)
	}

	syncResp, _ := ws.call(contracts.ActionStudioSyncWorkspace, contracts.StudioSyncWorkspacePayload{
		ProjectID: projID,
	})
	if !syncResp.Success {
		t.Fatalf("studio:sync_workspace failed: %+v", syncResp.Error)
	}

	epAfterSyncResp, _ := ws.call(contracts.ActionStudioEpisodeGet, contracts.StudioIDPayload{ID: epID})
	if !epAfterSyncResp.Success {
		t.Fatalf("studio:episode_get after sync failed: %+v", epAfterSyncResp.Error)
	}
	var epAfterSync contracts.StudioEpisodeDetailResult
	if err := json.Unmarshal(epAfterSyncResp.Data, &epAfterSync); err != nil {
		t.Fatalf("unmarshal episode after sync: %v", err)
	}
	if filepath.Clean(epAfterSync.Project.RootDir) != filepath.Clean(origRootDir) {
		t.Fatalf("security violation: smuggled root_dir overwrote project RootDir: %q != %q", epAfterSync.Project.RootDir, origRootDir)
	}
	if epAfterSync.Project.AutoApproveBatches {
		t.Fatalf("security violation: smuggled auto_approve_batches=true overwrote project setting via sync_workspace")
	}
	for _, a := range epAfterSync.Assets {
		if a.PrimaryPath != "" {
			t.Fatalf("security violation: smuggled outside primary_path survived sync_workspace: %q", a.PrimaryPath)
		}
	}
	for _, sh := range epAfterSync.Shots {
		if sh.KeyframePath != "" || sh.VideoPath != "" {
			t.Fatalf("security violation: smuggled shot media paths survived sync_workspace: keyframe=%q video=%q", sh.KeyframePath, sh.VideoPath)
		}
	}

	// Verify traversal slug on studio:project_create cannot escape ProjectsDir.
	travProjResp, _ := ws.call(contracts.ActionStudioProjectCreate, contracts.StudioProjectCreatePayload{
		Title: "Traversal Attempt",
		Slug:  "../../escaped_project",
	})
	if travProjResp.Success {
		var travDetail contracts.StudioProjectDetailResult
		if err := json.Unmarshal(travProjResp.Data, &travDetail); err != nil {
			t.Fatalf("unmarshal traversal project: %v", err)
		}
		projectsRoot := filepath.Join(tree.WorkspaceDir, "projects")
		rel, rerr := filepath.Rel(projectsRoot, travDetail.Project.RootDir)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("security violation: project RootDir %q escaped ProjectsDir %q", travDetail.Project.RootDir, projectsRoot)
		}
	}
}
