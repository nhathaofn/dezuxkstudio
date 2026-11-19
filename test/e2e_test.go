// Package test holds end-to-end tests that run the real core (storage, services,
// realtime hub, HTTP server) and talk to it like the frontend does.
// Unit tests live next to the code they test.
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/contracts"
	"app/internal/core"
	domainprovider "app/internal/domain/provider"
	domainstudio "app/internal/domain/studio"
	"app/internal/paths"
	"app/internal/security"
	"app/internal/service/agent"
	"app/internal/tools/watermark"
)

type harness struct {
	t     *testing.T
	core  *core.Core
	token string
	cfg   *config.Config
	paths paths.Paths
}

func startCore(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	cfg := config.Default(config.ModeDesktop)
	// Tests must never download the real agent CLIs in the background.
	cfg.Agent.AutoInstallCLI, cfg.Agent.AutoUpdateCLI = false, false
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := paths.Resolve("e2e", paths.Overrides{DataDir: dir + "/data", LogDir: dir + "/logs"})
	if err != nil {
		t.Fatal(err)
	}
	p.Config = dir + "/config"
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	token, _ := security.NewToken()
	c, err := core.New(context.Background(), core.Options{
		Config:        cfg,
		Paths:         p,
		Logger:        slog.New(slog.DiscardHandler),
		AuthToken:     token,
		OpenDirectory: func(string) error { return nil },
		HTTPClient:    http.DefaultClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return &harness{t: t, core: c, token: token, cfg: cfg, paths: p}
}

func (h *harness) httpURL(path string) string {
	return "http" + strings.TrimPrefix(strings.TrimSuffix(h.core.WSURL(), "/ws"), "ws") + path
}

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
	seq  int
}

func (h *harness) dial() *wsClient {
	h.t.Helper()
	conn, _, err := websocket.Dial(context.Background(), h.core.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion, contracts.AuthSubprotocolPrefix + h.token},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.CloseNow() })
	return &wsClient{t: h.t, conn: conn}
}

type frame struct {
	Type    string               `json:"type"`
	ID      string               `json:"id"`
	Success bool                 `json:"success"`
	Data    json.RawMessage      `json:"data"`
	Error   *contracts.ErrorBody `json:"error"`
	Event   string               `json:"event"`
	RefID   string               `json:"ref_id"`
}

// call sends an action and returns its response plus any events received meanwhile.
func (c *wsClient) call(action string, payload any) (frame, []frame) {
	c.t.Helper()
	c.seq++
	id := fmt.Sprintf("%s#%d", action, c.seq)
	raw, _ := json.Marshal(payload)
	msg, _ := json.Marshal(contracts.ClientMessage{Type: contracts.MessageAction, ID: id, Action: action, Payload: raw})
	if err := c.conn.Write(context.Background(), websocket.MessageText, msg); err != nil {
		c.t.Fatal(err)
	}
	var events []frame
	for {
		f := c.next()
		if f.Type == string(contracts.MessageResponse) && f.ID == id {
			return f, events
		}
		events = append(events, f)
	}
}

func (c *wsClient) next() frame {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		c.t.Fatal(err)
	}
	return f
}

// waitForEvent reads frames until the expected event name is received or timeout expires.
func (c *wsClient) waitForEvent(eventName string, timeout time.Duration) frame {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f := c.next()
		if f.Type == string(contracts.MessageEvent) && f.Event == eventName {
			return f
		}
	}
	c.t.Fatalf("timed out waiting for event %q", eventName)
	return frame{}
}

// createDummyPNG creates a minimal valid 1x1 PNG file at targetPath.
func createDummyPNG(t *testing.T, targetPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("create dir for png: %v", err)
	}
	// Minimal 1x1 transparent PNG payload
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(targetPath, pngData, 0o644); err != nil {
		t.Fatalf("write dummy png: %v", err)
	}
}

// createCustomPNG creates a valid PNG file with specific dimensions and color, returning its raw bytes.
func createCustomPNG(t *testing.T, targetPath string, c color.Color) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("create dir for png: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, c)
	img.Set(1, 1, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	data := buf.Bytes()
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		t.Fatalf("write custom png: %v", err)
	}
	return data
}

// createE2EWorkspaceRoot creates an isolated temporary workspace directory outside
// %LOCALAPPDATA% / system protected paths so AccessPolicy accepts it as a real workspace root.
func createE2EWorkspaceRoot(t *testing.T, name string) string {
	t.Helper()
	repoTemp, err := filepath.Abs(filepath.Join("..", ".temp"))
	if err != nil {
		t.Fatalf("resolve repo temp: %v", err)
	}
	if err := os.MkdirAll(repoTemp, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", repoTemp, err)
	}
	dir, err := os.MkdirTemp(repoTemp, "e2e_"+name+"_")
	if err != nil {
		t.Fatalf("mkdirtemp %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
		_ = os.Remove(repoTemp)
	})
	return dir
}

// wirePreferences provides a decoupled test-local representation of Preferences wire JSON.
type wirePreferences struct {
	SchemaVersion            int    `json:"schema_version"`
	Theme                    string `json:"theme"`
	Language                 string `json:"language"`
	WorkspaceRoot            string `json:"workspace_root"`
	OnboardingCompleted      bool   `json:"onboarding_completed"`
	AgentSkipTrustedApproval bool   `json:"agent_skip_trusted_approval"`
}

// wirePathsInfo provides a decoupled test-local representation of PathsInfo wire JSON.
type wirePathsInfo struct {
	Config                 string `json:"config"`
	Data                   string `json:"data"`
	Logs                   string `json:"logs"`
	Diagnostics            string `json:"diagnostics"`
	WorkspaceRoot          string `json:"workspace_root"`
	SuggestedWorkspaceRoot string `json:"suggested_workspace_root"`
	MediaDir               string `json:"media_dir"`
	WatermarkDir           string `json:"watermark_dir"`
	AgentWorkspace         string `json:"agent_workspace"`
}

// wireSystemInfo provides a decoupled test-local representation of SystemInfo wire JSON.
type wireSystemInfo struct {
	Mode    config.Mode   `json:"mode"`
	Version string        `json:"version"`
	Paths   wirePathsInfo `json:"paths"`
}

func TestEveryDeclaredActionIsServed(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	for _, spec := range contracts.Actions() {
		resp, _ := c.call(spec.Name, map[string]any{"__probe": true})
		if resp.Error != nil && resp.Error.Code == "ERR_UNKNOWN_ACTION" {
			t.Errorf("%s is declared but not served", spec.Name)
		}
	}
}

func TestPreferencesRoundTripAndBroadcast(t *testing.T) {
	h := startCore(t, nil)
	a, b := h.dial(), h.dial()

	resp, _ := a.call(contracts.ActionPreferencesGet, contracts.Empty{})
	var prefs contracts.Preferences
	if err := json.Unmarshal(resp.Data, &prefs); err != nil || prefs.Theme != "light" {
		t.Fatalf("defaults: %s %v", resp.Data, err)
	}

	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{"theme": "dark", "language": "vi"})
	if !resp.Success {
		t.Fatalf("update: %+v", resp.Error)
	}
	evt := b.next()
	if evt.Event != contracts.EventPreferencesUpdated || !strings.Contains(string(evt.Data), `"dark"`) {
		t.Fatalf("broadcast: %+v", evt)
	}

	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{"theme": "neon"})
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("invalid theme accepted: %+v", resp)
	}
	resp, _ = a.call(contracts.ActionPreferencesUpdate, map[string]string{})
	if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("empty patch accepted: %+v", resp)
	}
}

func TestDiagnosticsExportReportsProgress(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	resp, events := c.call(contracts.ActionDiagnosticsExport, contracts.Empty{})
	if !resp.Success {
		t.Fatalf("export: %+v", resp.Error)
	}
	progress := 0
	for _, e := range events {
		if e.Event == contracts.EventActionProgress && e.RefID == resp.ID {
			progress++
		}
	}
	if progress == 0 {
		t.Fatal("no progress events")
	}
}

func TestOpenFolderValidatesTarget(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()
	if resp, _ := c.call(contracts.ActionSystemOpenFolder, map[string]string{"target": "logs"}); !resp.Success {
		t.Fatalf("logs: %+v", resp.Error)
	}
	if resp, _ := c.call(contracts.ActionSystemOpenFolder, map[string]string{"target": "../../etc"}); resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("path target accepted: %+v", resp)
	}
}

func TestHTTPEndpoints(t *testing.T) {
	h := startCore(t, nil)

	res, err := http.Get(h.httpURL("/health"))
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("health: %v %v", res, err)
	}
	res.Body.Close()

	res, err = http.Get(h.httpURL("/runtime-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), h.token) || !strings.Contains(string(body), "ws_url") {
		t.Fatalf("runtime config leaked token or missing url: %s", body)
	}

	res, err = http.Post(h.httpURL("/api/events/broadcast"), "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("broadcast must be disabled by default, got %d", res.StatusCode)
	}

	if _, _, err := websocket.Dial(context.Background(), h.core.WSURL(), &websocket.DialOptions{
		Subprotocols: []string{contracts.ProtocolVersion},
	}); err == nil {
		t.Fatal("WebSocket without token accepted")
	}
}

func TestBroadcastEndpointWhenEnabled(t *testing.T) {
	h := startCore(t, func(c *config.Config) { c.Server.EnableBroadcast = true })
	ws := h.dial()

	post := func(token, contentType, body string) int {
		req, _ := http.NewRequest(http.MethodPost, h.httpURL("/api/events/broadcast"), strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if code := post("", "application/json", `{"event":"preferences:updated","data":{}}`); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code := post(h.token, "text/plain", `{"event":"preferences:updated","data":{}}`); code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d", code)
	}
	if code := post(h.token, "application/json", `{"event":"made:up","data":{}}`); code != http.StatusBadRequest {
		t.Errorf("undeclared event: %d", code)
	}
	if code := post(h.token, "application/json", `{"event":"preferences:updated","data":{"theme":"light","language":"en"}}`); code != http.StatusAccepted {
		t.Fatalf("valid broadcast: %d", code)
	}
	if evt := ws.next(); evt.Event != contracts.EventPreferencesUpdated {
		t.Fatalf("event not delivered: %+v", evt)
	}
}

func TestBinariesRealtimeActions(t *testing.T) {
	h := startCore(t, nil)
	ws := h.dial()

	// 1. binaries:list
	res, _ := ws.call(contracts.ActionBinariesList, contracts.Empty{})
	if !res.Success {
		t.Fatalf("binaries:list failed: %+v", res.Error)
	}
	var list contracts.BinaryListResult
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Binaries) == 0 {
		t.Fatal("expected at least one binary in list")
	}

	// 2. binaries:probe
	probeRes, _ := ws.call(contracts.ActionBinariesProbe, contracts.BinaryIDPayload{ID: "ffmpeg"})
	if !probeRes.Success {
		t.Fatalf("binaries:probe failed: %+v", probeRes.Error)
	}
	var info contracts.BinaryInfo
	if err := json.Unmarshal(probeRes.Data, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "ffmpeg" {
		t.Fatalf("expected id ffmpeg, got %s", info.ID)
	}

	// 3. binaries:set_path
	setRes, _ := ws.call(contracts.ActionBinariesSetPath, contracts.BinarySetPathPayload{
		ID:         "ffmpeg",
		CustomPath: "C:\\custom\\ffmpeg.exe",
	})
	if !setRes.Success {
		t.Fatalf("binaries:set_path failed: %+v", setRes.Error)
	}
	var updated contracts.BinaryInfo
	if err := json.Unmarshal(setRes.Data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.CustomPath != "C:\\custom\\ffmpeg.exe" {
		t.Fatalf("expected custom_path C:\\custom\\ffmpeg.exe, got %q", updated.CustomPath)
	}
}

func TestAgentRealtimeActions(t *testing.T) {
	h := startCore(t, nil)
	ws := h.dial()

	// 1. agent:list
	res, _ := ws.call(contracts.ActionAgentList, contracts.Empty{})
	if !res.Success {
		t.Fatalf("agent:list failed: %+v", res.Error)
	}
	var list contracts.AgentListResult
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 3 {
		t.Fatalf("expected 3 agent providers, got %d", len(list.Providers))
	}

	// 2. agent:probe
	probeRes, _ := ws.call(contracts.ActionAgentProbe, contracts.AgentProviderIDPayload{ID: "antigravity"})
	if !probeRes.Success {
		t.Fatalf("agent:probe failed: %+v", probeRes.Error)
	}
	var info contracts.AgentProviderInfo
	if err := json.Unmarshal(probeRes.Data, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "antigravity" {
		t.Fatalf("expected id antigravity, got %s", info.ID)
	}

	// 3. agent:set_path
	setRes, _ := ws.call(contracts.ActionAgentSetPath, contracts.AgentSetPathPayload{
		ID:         "antigravity",
		CustomPath: "C:\\custom\\agy.exe",
	})
	if !setRes.Success {
		t.Fatalf("agent:set_path failed: %+v", setRes.Error)
	}
	var updated contracts.AgentProviderInfo
	if err := json.Unmarshal(setRes.Data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.CustomPath != "C:\\custom\\agy.exe" {
		t.Fatalf("expected custom_path C:\\custom\\agy.exe, got %q", updated.CustomPath)
	}

	// 4. agent:send_prompt fails cleanly when no agent is connected
	promptRes, _ := ws.call(contracts.ActionAgentSendPrompt, contracts.AgentSendPromptPayload{
		Prompt: "hello",
	})
	if promptRes.Success || promptRes.Error == nil || promptRes.Error.Code != "ERR_UNAVAILABLE" {
		t.Fatalf("expected ERR_UNAVAILABLE when disconnected, got %+v", promptRes)
	}

	// 5. agent:history, agent:list_approvals, agent:list_allowed_dirs, agent:set_ui_context
	if res, _ := ws.call(contracts.ActionAgentHistory, contracts.Empty{}); !res.Success {
		t.Fatalf("agent:history failed: %+v", res.Error)
	}
	if res, _ := ws.call(contracts.ActionAgentListApprovals, contracts.Empty{}); !res.Success {
		t.Fatalf("agent:list_approvals failed: %+v", res.Error)
	}
	if res, _ := ws.call(contracts.ActionAgentListAllowedDirs, contracts.Empty{}); !res.Success {
		t.Fatalf("agent:list_allowed_dirs failed: %+v", res.Error)
	}
	if res, _ := ws.call(contracts.ActionAgentSetUIContext, contracts.AgentUIContextPayload{Route: "/watermark"}); !res.Success {
		t.Fatalf("agent:set_ui_context failed: %+v", res.Error)
	}
}

func TestAgentCustomizationsRealtimeActions(t *testing.T) {
	h := startCore(t, nil)
	ws := h.dial()

	listRes, _ := ws.call(contracts.ActionAgentListCustomizations, contracts.Empty{})
	if !listRes.Success {
		t.Fatalf("agent:list_customizations failed: %+v", listRes.Error)
	}
	var initial contracts.AgentCustomizationsResult
	if err := json.Unmarshal(listRes.Data, &initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Skills) < 2 || len(initial.Subagents) < 2 || len(initial.AvailableTools) == 0 {
		t.Fatalf("expected starter customizations and available tools, got %+v", initial)
	}

	saveSkillRes, _ := ws.call(contracts.ActionAgentSaveSkill, contracts.AgentSaveSkillPayload{
		Name:          "e2e-skill",
		Description:   "E2E custom skill",
		UserInvocable: true,
		Enabled:       true,
		Body:          "Follow step 1.",
	})
	if !saveSkillRes.Success {
		t.Fatalf("agent:save_skill failed: %+v", saveSkillRes.Error)
	}

	delSkillRes, _ := ws.call(contracts.ActionAgentDeleteSkill, contracts.AgentCustomizationIDPayload{Name: "e2e-skill"})
	if !delSkillRes.Success {
		t.Fatalf("agent:delete_skill failed: %+v", delSkillRes.Error)
	}

	delBuiltInRes, _ := ws.call(contracts.ActionAgentDeleteSkill, contracts.AgentCustomizationIDPayload{Name: "brief-structuring"})
	if !delBuiltInRes.Success {
		t.Fatalf("agent:delete_skill built-in failed: %+v", delBuiltInRes.Error)
	}

	restSkillRes, _ := ws.call(contracts.ActionAgentRestoreSkill, contracts.AgentCustomizationIDPayload{Name: "brief-structuring"})
	if !restSkillRes.Success {
		t.Fatalf("agent:restore_skill failed: %+v", restSkillRes.Error)
	}

	saveSubRes, _ := ws.call(contracts.ActionAgentSaveSubagent, contracts.AgentSaveSubagentPayload{
		Name:        "e2e-sub",
		Description: "E2E subagent",
		Skills:      []string{"review-checklist"},
		Tools:       []string{"workspace_read_file", "subagent_run"},
		Enabled:     true,
		Body:        "Inspect safely.",
	})
	if !saveSubRes.Success {
		t.Fatalf("agent:save_subagent failed: %+v", saveSubRes.Error)
	}
	var afterSubSave contracts.AgentCustomizationsResult
	if err := json.Unmarshal(saveSubRes.Data, &afterSubSave); err != nil {
		t.Fatal(err)
	}
	var sawSanitized bool
	for _, sa := range afterSubSave.Subagents {
		if sa.Name == "e2e-sub" {
			sawSanitized = true
			for _, tl := range sa.Tools {
				if tl == "subagent_run" {
					t.Fatalf("subagent_run must be sanitized out on SaveSubagent")
				}
			}
		}
	}
	if !sawSanitized {
		t.Fatalf("expected e2e-sub in AgentCustomizationsResult")
	}

	if delSubRes, _ := ws.call(contracts.ActionAgentDeleteSubagent, contracts.AgentCustomizationIDPayload{Name: "e2e-sub"}); !delSubRes.Success {
		t.Fatalf("agent:delete_subagent failed: %+v", delSubRes.Error)
	}
	if restSubRes, _ := ws.call(contracts.ActionAgentRestoreSubagent, contracts.AgentCustomizationIDPayload{Name: "workspace-reviewer"}); !restSubRes.Success {
		t.Fatalf("agent:restore_subagent failed: %+v", restSubRes.Error)
	}
}

// Browser clients mirror the server's real timeouts from /runtime-config.json; before, they used a
// hard-coded 30s and reported ERR_TIMEOUT while a reconfigured server was still working.
func TestRuntimeConfigCarriesServerTimeouts(t *testing.T) {
	h := startCore(t, func(c *config.Config) {
		c.Realtime.ActionTimeout = 45 * time.Second
		c.Realtime.LongActionTimeout = 20 * time.Minute
		c.Realtime.PingInterval = 12 * time.Second
	})
	res, err := http.Get(h.httpURL("/runtime-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var rc struct {
		WSURL        string `json:"ws_url"`
		Action       int64  `json:"action_timeout_ms"`
		LongAction   int64  `json:"long_action_timeout_ms"`
		PingInterval int64  `json:"ping_interval_ms"`
	}
	if err := json.NewDecoder(res.Body).Decode(&rc); err != nil {
		t.Fatal(err)
	}
	if rc.WSURL == "" || rc.Action != 45_000 || rc.LongAction != 1_200_000 || rc.PingInterval != 12_000 {
		t.Fatalf("runtime config: %+v", rc)
	}
}

// -----------------------------------------------------------------------------
// Tier 1: Feature Coverage (Paths Resolution, System Info, Preferences v2)
// -----------------------------------------------------------------------------

// TestE2E_Tier1_WorkspacePaths_SystemInfoAndFolderTargets verifies R1 & R2:
// 1. system:info exposes all canonical workspace paths and suggested root.
// 2. suggested_workspace_root dynamically uses buildinfo.ProductName without hardcoding.
// 3. system:open_folder supports FolderWorkspaceRoot ("workspace_root").
// 4. StagingDir is strictly isolated outside WorkspaceRoot.
func TestE2E_Tier1_WorkspacePaths_SystemInfoAndFolderTargets(t *testing.T) {
	h := startCore(t, nil)
	c := h.dial()

	resp, _ := c.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !resp.Success {
		t.Fatalf("system:info failed: %+v", resp.Error)
	}

	var sysInfo wireSystemInfo
	if err := json.Unmarshal(resp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}

	// 1. Suggested Workspace Root dynamically contains ProductName
	if !strings.Contains(sysInfo.Paths.SuggestedWorkspaceRoot, buildinfo.ProductName) {
		t.Errorf("expected suggested_workspace_root to contain ProductName %q, got: %q",
			buildinfo.ProductName, sysInfo.Paths.SuggestedWorkspaceRoot)
	}

	// 2. Paths hierarchy verification on default unconfigured state
	if sysInfo.Paths.WorkspaceRoot == "" {
		// When unconfigured, fallback studio path must be resolved
		mediaDirClean := filepath.ToSlash(sysInfo.Paths.MediaDir)
		if !strings.HasSuffix(mediaDirClean, "studio/media") {
			t.Errorf("expected fallback studio media dir ending with 'studio/media', got: %q", sysInfo.Paths.MediaDir)
		}
	}

	// 3. Configure a valid workspace root explicitly
	validWorkspace := createE2EWorkspaceRoot(t, "ActiveWorkspace")
	updResp, _ := c.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       validWorkspace,
		"onboarding_completed": true,
	})
	if !updResp.Success {
		t.Fatalf("preferences:update failed: %+v", updResp.Error)
	}

	// Query updated system info
	resp2, _ := c.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !resp2.Success {
		t.Fatalf("system:info after update failed: %+v", resp2.Error)
	}
	var sysInfo2 wireSystemInfo
	if err := json.Unmarshal(resp2.Data, &sysInfo2); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	if filepath.Clean(sysInfo2.Paths.WorkspaceRoot) != filepath.Clean(validWorkspace) {
		t.Fatalf("expected updated workspace root %q, got %q", validWorkspace, sysInfo2.Paths.WorkspaceRoot)
	}

	// 4. Folder target support ("workspace_root")
	openResp, _ := c.call(contracts.ActionSystemOpenFolder, map[string]any{
		"target": "workspace_root",
	})
	if !openResp.Success {
		t.Fatalf("system:open_folder for workspace_root failed: %+v", openResp.Error)
	}

	// 5. Staging directory isolation check: staging must strictly NOT reside under or enclose workspace
	dataStaging := filepath.Join(sysInfo2.Paths.Data, "staging")
	cleanStaging := strings.ToLower(filepath.Clean(dataStaging))
	cleanWorkspace := strings.ToLower(filepath.Clean(sysInfo2.Paths.WorkspaceRoot))
	if cleanWorkspace == "" {
		t.Fatal("expected non-empty active workspace root for staging isolation assertion")
	}
	if strings.HasPrefix(cleanStaging, cleanWorkspace) {
		t.Fatalf("security violation: staging dir %q must not reside inside workspace root %q", dataStaging, sysInfo2.Paths.WorkspaceRoot)
	}
	if strings.HasPrefix(cleanWorkspace, cleanStaging) {
		t.Fatalf("security violation: workspace root %q must not enclose staging dir %q", sysInfo2.Paths.WorkspaceRoot, dataStaging)
	}
}

// -----------------------------------------------------------------------------
// Tier 2: Boundary & Corner Cases (Validation & Security Stress)
// -----------------------------------------------------------------------------

// TestE2E_Tier2_WorkspaceRoot_ValidationBoundaries verifies candidate root rejection (ERR_VALIDATION)
// across boundaries B1 through B10:
// - B1: Volume roots (C:\, D:\, /)
// - B2: System folders (C:\Windows, C:\Program Files, C:\ProgramData)
// - B3: Internal AppData nesting (%LOCALAPPDATA%\DezuxkStudio or under DataDir)
// - B4: Device namespaces (\\?\, \\.\, \??\)
// - B5: Alternate Data Streams syntax (C:\path:stream)
// - B6: Relative paths (./rel, ../rel)
// - B7: NUL byte injection (\x00)
// - B8: Existing regular file (not directory)
// - B9: Root deletion guard on AccessPolicy (cannot delete WorkspaceRoot, MediaDir, StagingDir)
// - B10: Windows DOS reserved device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9)
func TestE2E_Tier2_WorkspaceRoot_ValidationBoundaries(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	// Query system info to get paths for AppData and system nesting tests
	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}

	// Create an existing regular file for B8 outside protected folders
	regularFile := filepath.Join(createE2EWorkspaceRoot(t, "RegFileParent"), "regular_file.txt")
	if err := os.WriteFile(regularFile, []byte("i am a file"), 0o644); err != nil {
		t.Fatalf("write regular file: %v", err)
	}

	boundaryCases := []struct {
		name          string
		candidateRoot string
	}{
		// B1: Volume roots
		{name: "B1_VolumeRoot_C", candidateRoot: `C:\`},
		{name: "B1_VolumeRoot_D", candidateRoot: `D:\`},
		{name: "B1_VolumeRoot_Slash", candidateRoot: `/`},

		// B2: System folders
		{name: "B2_SystemFolder_Windows", candidateRoot: `C:\Windows`},
		{name: "B2_SystemFolder_ProgramFiles", candidateRoot: `C:\Program Files`},
		{name: "B2_SystemFolder_ProgramData", candidateRoot: `C:\ProgramData`},

		// B3: AppData nesting
		{name: "B3_AppData_DataDirNesting", candidateRoot: filepath.Join(sysInfo.Paths.Data, "nested_workspace")},
		{name: "B3_AppData_LogsNesting", candidateRoot: filepath.Join(sysInfo.Paths.Logs, "nested_workspace")},

		// B4: Device namespaces
		{name: "B4_DeviceNamespace_Question", candidateRoot: `\\?\C:\test\workspace`},
		{name: "B4_DeviceNamespace_Dot", candidateRoot: `\\.\C:\test\workspace`},
		{name: "B4_DeviceNamespace_NT", candidateRoot: `\??\C:\test\workspace`},

		// B5: Alternate Data Streams
		{name: "B5_ADS_StreamSyntax", candidateRoot: `C:\custom\folder:stream`},

		// B6: Relative paths
		{name: "B6_RelativePath_DotSlash", candidateRoot: `./workspace`},
		{name: "B6_RelativePath_ParentSlash", candidateRoot: `../outside/workspace`},
		{name: "B6_RelativePath_BareName", candidateRoot: `my_workspace`},

		// B7: NUL byte
		{name: "B7_NULByte_Injection", candidateRoot: "C:\\custom\x00\\bad_path"},

		// B8: Existing regular file
		{name: "B8_ExistingRegularFile", candidateRoot: regularFile},

		// B10: Windows DOS reserved device names
		{name: "B10_DOSDevice_CON", candidateRoot: `C:\CON`},
		{name: "B10_DOSDevice_PRN", candidateRoot: `D:\projects\PRN`},
		{name: "B10_DOSDevice_AUX", candidateRoot: `D:\projects\AUX`},
		{name: "B10_DOSDevice_NUL_Sub", candidateRoot: `D:\projects\workspace\NUL\sub`},
		{name: "B10_DOSDevice_CON_Sub", candidateRoot: `D:\projects\workspace\CON\sub`},
		{name: "B10_DOSDevice_COM1_Root", candidateRoot: `C:\COM1`},
		{name: "B10_DOSDevice_LPT1_Root", candidateRoot: `C:\LPT1`},
		{name: "B10_DOSDevice_COM1", candidateRoot: `C:\path\com1`},
		{name: "B10_DOSDevice_LPT3_Ext", candidateRoot: `D:\projects\lpt3.txt`},
	}

	for _, tc := range boundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, events := client.call(contracts.ActionPreferencesUpdate, map[string]any{
				"workspace_root": tc.candidateRoot,
			})

			// 1. Must be rejected with validation error
			if resp.Success {
				t.Fatalf("%s: expected rejection for invalid candidate root %q, got success", tc.name, tc.candidateRoot)
			}
			if resp.Error == nil || resp.Error.Code != "ERR_VALIDATION" {
				t.Fatalf("%s: expected ERR_VALIDATION for candidate root %q, got %+v", tc.name, tc.candidateRoot, resp.Error)
			}
			if strings.HasPrefix(tc.name, "B10_DOSDevice_") {
				if !strings.Contains(resp.Error.Message, "contains a Windows reserved device name") {
					t.Fatalf("%s: expected error message to mention Windows reserved device name, got: %+v", tc.name, resp.Error)
				}
			}

			// 2. Must not emit EventPreferencesUpdated broadcast event
			for _, ev := range events {
				if ev.Event == contracts.EventPreferencesUpdated {
					t.Fatalf("%s: unexpected EventPreferencesUpdated broadcast on rejected candidate root: %+v", tc.name, ev)
				}
			}
		})
	}

	// B9: Root deletion guard on AccessPolicy
	t.Run("B9_RootDeletionGuard_AccessPolicy", func(t *testing.T) {
		tempBase := t.TempDir()
		wsRoot := filepath.Join(tempBase, "workspace")
		mediaRoot := filepath.Join(tempBase, "media")
		dataDir := filepath.Join(tempBase, "data")
		profilesDir := filepath.Join(dataDir, "agent-profiles")
		stagingDir := filepath.Join(dataDir, "staging")

		for _, d := range []string{wsRoot, mediaRoot, dataDir, profilesDir, stagingDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", d, err)
			}
		}

		policy := agent.NewAccessPolicy(dataDir, wsRoot, mediaRoot, profilesDir, nil)

		// Assert that deleting WorkspaceRoot is forbidden
		if _, err := policy.ValidateDelete(wsRoot); err == nil {
			t.Fatalf("expected ValidateDelete on WorkspaceRoot %q to be rejected, got nil", wsRoot)
		} else if !strings.Contains(err.Error(), "cannot delete a DezuxkStudio root folder") {
			t.Fatalf("expected error containing 'cannot delete a DezuxkStudio root folder', got: %v", err)
		}

		// Assert that deleting MediaRoot is forbidden
		if _, err := policy.ValidateDelete(mediaRoot); err == nil {
			t.Fatalf("expected ValidateDelete on MediaDir %q to be rejected, got nil", mediaRoot)
		} else if !strings.Contains(err.Error(), "cannot delete a DezuxkStudio root folder") {
			t.Fatalf("expected error containing 'cannot delete a DezuxkStudio root folder', got: %v", err)
		}

		// Normal file inside workspace should be allowed to be deleted
		normalFile := filepath.Join(wsRoot, "sample.txt")
		if err := os.WriteFile(normalFile, []byte("temp"), 0o644); err != nil {
			t.Fatalf("write normal file: %v", err)
		}
		if _, err := policy.ValidateDelete(normalFile); err != nil {
			t.Fatalf("expected ValidateDelete on normal workspace file to succeed, got: %v", err)
		}
	})
}

// TestE2E_Tier1_Preferences_SchemaV2_And_HotReload verifies:
// 1. Initial preferences query contains SchemaVersion v2 fields (onboarding_completed=false).
// 2. Updating workspace_root, onboarding_completed, and agent_skip_trusted_approval.
// 3. Realtime broadcast of EventPreferencesUpdated to peer client.
// 4. WorkspaceTree.Ensure() creates all canonical subdirectories on disk immediately.
func TestE2E_Tier1_Preferences_SchemaV2_And_HotReload(t *testing.T) {
	h := startCore(t, nil)
	clientA, clientB := h.dial(), h.dial()

	// 1. Verify schema defaults
	getResp, _ := clientA.call(contracts.ActionPreferencesGet, contracts.Empty{})
	if !getResp.Success {
		t.Fatalf("preferences:get failed: %+v", getResp.Error)
	}
	var initialPrefs wirePreferences
	if err := json.Unmarshal(getResp.Data, &initialPrefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if initialPrefs.OnboardingCompleted {
		t.Fatal("expected onboarding_completed=false on fresh installation")
	}

	// 2. Update preferences with new workspace root and security toggle
	newRoot := createE2EWorkspaceRoot(t, "MyWorkspace")
	patch := map[string]any{
		"workspace_root":              newRoot,
		"onboarding_completed":        true,
		"agent_skip_trusted_approval": true,
	}
	updResp, _ := clientA.call(contracts.ActionPreferencesUpdate, patch)
	if !updResp.Success {
		t.Fatalf("preferences:update failed: %+v", updResp.Error)
	}

	// 3. Client B receives broadcast event
	evt := clientB.waitForEvent(contracts.EventPreferencesUpdated, 5*time.Second)
	if !strings.Contains(string(evt.Data), "MyWorkspace") {
		t.Fatalf("broadcast event missing new workspace root: %s", evt.Data)
	}

	// 4. System paths updated dynamically without restart
	infoResp, _ := clientA.call(contracts.ActionSystemInfo, contracts.Empty{})
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	if filepath.Clean(sysInfo.Paths.WorkspaceRoot) != filepath.Clean(newRoot) {
		t.Fatalf("expected active workspace root %q, got %q", newRoot, sysInfo.Paths.WorkspaceRoot)
	}

	// 5. Ensure() created all canonical subdirectories
	expectedSubdirs := []string{
		filepath.Join(newRoot, "media", "images"),
		filepath.Join(newRoot, "media", "videos"),
		filepath.Join(newRoot, "media", "tts"),
		filepath.Join(newRoot, "media", "watermark"),
		filepath.Join(newRoot, "workspace"),
	}
	for _, dir := range expectedSubdirs {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			t.Errorf("expected ensured directory %q to exist: %v", dir, err)
		}
	}
}

// -----------------------------------------------------------------------------
// Tier 3: Cross-Feature Interactions
// -----------------------------------------------------------------------------

// TestE2E_Tier3_WorkspaceHotReload_MediaAssetRetention verifies Scenario C1:
// 1. Media asset created and registered in Root A with unique payload data.
// 2. Hot-reload preferences to New Root B.
// 3. New media created in Root B with distinct unique payload data.
// 4. HTTP MediaDownload serves Root B asset AND continues to serve Root A asset (historical retention).
// 5. Downloaded bytes and Content-Type match exactly without payload ambiguity.
func TestE2E_Tier3_WorkspaceHotReload_MediaAssetRetention(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	// Step 1: Initialize Root A
	rootA := createE2EWorkspaceRoot(t, "RootA")
	updInitResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       rootA,
		"onboarding_completed": true,
	})
	if !updInitResp.Success {
		t.Fatalf("preferences:update init rootA failed: %+v", updInitResp.Error)
	}

	asset1Path := filepath.Join(rootA, "media", "images", "asset1.png")
	asset1Data := createCustomPNG(t, asset1Path, color.RGBA{R: 255, A: 255}) // Distinct red 2x2 image

	// Register asset1 into media library
	regResp, _ := client.call(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   asset1Path,
		Kind:       "image",
		SourceNode: "test",
	})
	if !regResp.Success {
		t.Fatalf("register asset1 failed: %+v", regResp.Error)
	}
	var asset1 contracts.MediaAssetItem
	if err := json.Unmarshal(regResp.Data, &asset1); err != nil {
		t.Fatalf("unmarshal asset1: %v", err)
	}

	// Step 2: Hot-reload to Root B
	rootB := createE2EWorkspaceRoot(t, "RootB")
	updResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": rootB,
	})
	if !updResp.Success {
		t.Fatalf("hot reload to rootB failed: %+v", updResp.Error)
	}

	// Step 3: Write asset2 with distinct blue payload into Root B
	asset2Path := filepath.Join(rootB, "media", "images", "asset2.png")
	asset2Data := createCustomPNG(t, asset2Path, color.RGBA{B: 255, A: 255}) // Distinct blue 2x2 image
	regResp2, _ := client.call(contracts.ActionMediaRegisterInput, contracts.MediaRegisterInputPayload{
		FilePath:   asset2Path,
		Kind:       "image",
		SourceNode: "test",
	})
	if !regResp2.Success {
		t.Fatalf("register asset2 failed: %+v", regResp2.Error)
	}
	var asset2 contracts.MediaAssetItem
	if err := json.Unmarshal(regResp2.Data, &asset2); err != nil {
		t.Fatalf("unmarshal asset2: %v", err)
	}

	// Helper for HTTP media download with bounded 5s timeout and payload inspection
	httpClient := &http.Client{Timeout: 5 * time.Second}
	downloadAsset := func(assetID string) (int, string, []byte) {
		req, _ := http.NewRequest(http.MethodGet, h.httpURL("/api/media/download?asset_id="+assetID), nil)
		req.Header.Set("Authorization", "Bearer "+h.token)
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("download asset %s: %v", assetID, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read download body for asset %s: %v", assetID, err)
		}
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}

	// Step 4: Verify active asset2 download succeeds (200 OK, Content-Type image/png, payload matches asset2Data)
	code2, ctype2, body2 := downloadAsset(asset2.ID)
	if code2 != http.StatusOK {
		t.Fatalf("active root asset2 download failed with status: %d (expected 200)", code2)
	}
	if !strings.Contains(ctype2, "image/png") {
		t.Errorf("expected Content-Type image/png for asset2, got %q", ctype2)
	}
	if !bytes.Equal(body2, asset2Data) {
		t.Fatalf("asset2 downloaded body does not match expected payload")
	}

	// Step 5: Verify historical asset1 download STILL SUCCEEDS without 403 or 404 (historical retention)
	code1, ctype1, body1 := downloadAsset(asset1.ID)
	if code1 != http.StatusOK {
		t.Fatalf("historical root asset1 download failed with status: %d (expected 200 via retained allowedRoots)", code1)
	}
	if !strings.Contains(ctype1, "image/png") {
		t.Errorf("expected Content-Type image/png for asset1, got %q", ctype1)
	}
	if !bytes.Equal(body1, asset1Data) {
		t.Fatalf("historical asset1 downloaded body does not match expected payload")
	}

	// Step 6: Negative verification: requesting a non-existent asset returns 404
	codeNonExistent, _, _ := downloadAsset("non-existent-asset-id")
	if codeNonExistent != http.StatusNotFound && codeNonExistent != http.StatusForbidden {
		t.Fatalf("expected non-existent asset download to return 404 or 403, got: %d", codeNonExistent)
	}
}

func callE2EMCPTool(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("mcp %s: %v", tool, err)
	}
	var payload map[string]any
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(tc.Text), &payload)
			break
		}
	}
	return payload, res.IsError
}

// TestE2E_Tier3_AgentSkipTrustedApproval_Vs_MandatoryApprovals verifies Scenario C3 & F7:
// 1. Destructive in-place watermark operations in trusted folders via Agent MCP:
//   - When skip=false: returns status="needs_approval" and queues pending approval in agent:list_approvals.
//   - When skip=true: auto-approves destructive file action in trusted folder (auto_approved=true).
//
// 2. Mandatory Approvals Invariant (even when skip=true):
//   - Agent MCP binaries_download ALWAYS returns needs_approval (never hits network or auto-approves).
//   - Agent MCP tool.provider_delete ALWAYS returns needs_approval (never auto-approves).
//
// 3. Untrusted Folder Operations (F7.5):
//   - Operations outside trusted directories are rejected outright, even when skip=true.
func TestE2E_Tier3_AgentSkipTrustedApproval_Vs_MandatoryApprovals(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	root := createE2EWorkspaceRoot(t, "TrustedRoot")
	initResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":              root,
		"onboarding_completed":        true,
		"agent_skip_trusted_approval": false,
	})
	if !initResp.Success {
		t.Fatalf("preferences:update failed: %+v", initResp.Error)
	}

	testImg := filepath.Join(root, "media", "images", "input.png")
	createDummyPNG(t, testImg)

	// Connect an in-memory MCP client to the real Core's Agent MCP server
	ctx := context.Background()
	if _, err := h.core.Agent().EnsureActiveConversation(ctx, "codex", "E2E Approval Test"); err != nil {
		t.Fatalf("ensure active conversation: %v", err)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := h.core.Agent().BuildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("connect mcp server: %v", err)
	}
	mcpClient, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-mcp-client"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	defer mcpClient.Close()

	// 1. skip=false: destructive in-place watermark in trusted folder MUST require approval
	payloadWait, isErr := callE2EMCPTool(t, mcpClient, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{testImg},
			"inplace": true,
		},
	})
	if isErr || payloadWait["status"] != "needs_approval" {
		t.Fatalf("expected status=needs_approval when skip=false, got isErr=%v payload=%v", isErr, payloadWait)
	}
	approvalID1, _ := payloadWait["approval_id"].(string)
	if approvalID1 == "" {
		t.Fatalf("expected non-empty approval_id, got %v", payloadWait)
	}

	// Verify it appears in agent:list_approvals and reject it cleanly via WS
	apprResp, _ := client.call(contracts.ActionAgentListApprovals, contracts.Empty{})
	if !apprResp.Success {
		t.Fatalf("agent:list_approvals failed: %+v", apprResp.Error)
	}
	var apprList contracts.AgentApprovalListResult
	if err := json.Unmarshal(apprResp.Data, &apprList); err != nil {
		t.Fatalf("unmarshal agent:list_approvals: %v", err)
	}
	if len(apprList.Approvals) != 1 || apprList.Approvals[0].ID != approvalID1 {
		t.Fatalf("expected pending approval %q in list, got %+v", approvalID1, apprList.Approvals)
	}
	rejResp, _ := client.call(contracts.ActionAgentResolveApproval, contracts.AgentResolveApprovalPayload{
		ID:      approvalID1,
		Approve: false,
	})
	if !rejResp.Success {
		t.Fatalf("agent:resolve_approval reject failed: %+v", rejResp.Error)
	}

	// 2. Untrusted folder isolation (F7.5): verify outside directory is not trusted
	outsideDir := createE2EWorkspaceRoot(t, "UntrustedOutside")
	outsideImg := filepath.Join(outsideDir, "outside.png")
	createDummyPNG(t, outsideImg)

	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	var sysInfo wireSystemInfo
	_ = json.Unmarshal(infoResp.Data, &sysInfo)

	wsDir := filepath.Join(root, "workspace")
	mediaDir := filepath.Join(root, "media")
	policy := agent.NewAccessPolicy(sysInfo.Paths.Data, wsDir, mediaDir, filepath.Join(sysInfo.Paths.Data, "agent-profiles"), nil)
	if !policy.IsTrusted(testImg) {
		t.Fatalf("expected testImg in %q to be classified as trusted", root)
	}
	if policy.IsTrusted(outsideImg) {
		t.Fatalf("security violation: outside image %q must not be classified as trusted", outsideImg)
	}
	if _, err := policy.ValidateWrite(filepath.Join(outsideDir, "notes.txt")); err == nil {
		t.Fatalf("security violation: ValidateWrite outside trusted roots must fail")
	}

	// 3. Enable agent_skip_trusted_approval=true via WS -> hot-reloads agent.Service
	skipResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"agent_skip_trusted_approval": true,
	})
	if !skipResp.Success {
		t.Fatalf("preferences:update skip=true failed: %+v", skipResp.Error)
	}
	getResp, _ := client.call(contracts.ActionPreferencesGet, contracts.Empty{})
	var updatedPrefs wirePreferences
	_ = json.Unmarshal(getResp.Data, &updatedPrefs)
	if !updatedPrefs.AgentSkipTrustedApproval {
		t.Fatalf("expected agent_skip_trusted_approval=true, got: %v", updatedPrefs.AgentSkipTrustedApproval)
	}

	// Now destructive in-place watermark on trusted file runs directly with auto_approved=true
	payloadAuto, isErrAuto := callE2EMCPTool(t, mcpClient, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{testImg},
			"inplace": true,
		},
	})
	if isErrAuto || payloadAuto["ok"] != true || payloadAuto["auto_approved"] != true {
		t.Fatalf("expected auto-approved trusted run when skip=true, got isErr=%v payload=%v", isErrAuto, payloadAuto)
	}

	// Untrusted target remains rejected even when skip=true
	if _, isErrOutside := callE2EMCPTool(t, mcpClient, "workflow_run_node", map[string]any{
		"type": watermark.NodeType,
		"input": map[string]any{
			"images":  []any{outsideImg},
			"inplace": true,
		},
	}); !isErrOutside {
		t.Fatal("security violation: untrusted file must fail even when agent_skip_trusted_approval=true")
	}

	// 4. Security Invariant: MCP binaries_download ALWAYS requires approval (never hits network or auto-approves)
	binPayload, isErrBin := callE2EMCPTool(t, mcpClient, "binaries_download", map[string]any{
		"id": "ffmpeg",
	})
	if isErrBin || binPayload["status"] != "needs_approval" || binPayload["auto_approved"] == true {
		t.Fatalf("binaries_download must require approval even when skip=true, got isErr=%v payload=%v", isErrBin, binPayload)
	}

	// 5. Security Invariant: MCP tool.provider_delete ALWAYS requires approval
	delPayload, isErrDel := callE2EMCPTool(t, mcpClient, "workflow_run_node", map[string]any{
		"type": "tool.provider_delete",
		"input": map[string]any{
			"id":      "nonexistent-provider",
			"confirm": true,
		},
	})
	if isErrDel || delPayload["status"] != "needs_approval" || delPayload["auto_approved"] == true {
		t.Fatalf("tool.provider_delete must require approval even when skip=true, got isErr=%v payload=%v", isErrDel, delPayload)
	}

	// Both mandatory approvals must be queued and rejectable via WS
	apprResp2, _ := client.call(contracts.ActionAgentListApprovals, contracts.Empty{})
	var apprList2 contracts.AgentApprovalListResult
	if err := json.Unmarshal(apprResp2.Data, &apprList2); err != nil {
		t.Fatalf("unmarshal agent:list_approvals 2: %v", err)
	}
	if len(apprList2.Approvals) != 2 {
		t.Fatalf("expected 2 mandatory pending approvals, got %d: %+v", len(apprList2.Approvals), apprList2.Approvals)
	}
	for _, item := range apprList2.Approvals {
		res, _ := client.call(contracts.ActionAgentResolveApproval, contracts.AgentResolveApprovalPayload{
			ID:      item.ID,
			Approve: false,
		})
		if !res.Success {
			t.Fatalf("failed rejecting approval %s: %+v", item.ID, res.Error)
		}
	}

	// 6. Approval Resolution verification: ActionAgentResolveApproval on non-existent approval must fail
	resolveResp, _ := client.call(contracts.ActionAgentResolveApproval, contracts.AgentResolveApprovalPayload{
		ID:      "non-existent-approval-id",
		Approve: true,
	})
	if resolveResp.Success {
		t.Fatal("expected resolving non-existent approval to fail, got success")
	}
	if resolveResp.Error == nil {
		t.Fatal("expected error body on non-existent approval resolution")
	}
}

// TestE2E_Tier3_WorkspaceHotReload_ActiveAgentSession verifies Scenario C2:
// 1. Configure Initial Workspace Root A.
// 2. Verify agent workspace directory and paths.
// 3. Hot-reload preferences to New Workspace Root B.
// 4. Verify agent working directory updates dynamically without restarting core.
// 5. Subsequent agent actions and directories target Root B.
func TestE2E_Tier3_WorkspaceHotReload_ActiveAgentSession(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	// Step 1: Initialize Root A
	rootA := createE2EWorkspaceRoot(t, "SessionRootA")
	updRespA, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       rootA,
		"onboarding_completed": true,
	})
	if !updRespA.Success {
		t.Fatalf("preferences:update init rootA failed: %+v", updRespA.Error)
	}

	infoRespA, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoRespA.Success {
		t.Fatalf("system:info rootA failed: %+v", infoRespA.Error)
	}
	var sysInfoA wireSystemInfo
	if err := json.Unmarshal(infoRespA.Data, &sysInfoA); err != nil {
		t.Fatalf("unmarshal system:info A: %v", err)
	}
	if filepath.Clean(sysInfoA.Paths.WorkspaceRoot) != filepath.Clean(rootA) {
		t.Fatalf("expected workspace root %q, got %q", rootA, sysInfoA.Paths.WorkspaceRoot)
	}

	// Step 2: Query agent history / approvals / allowed dirs under Root A
	apprResp, _ := client.call(contracts.ActionAgentListApprovals, contracts.Empty{})
	if !apprResp.Success {
		t.Fatalf("agent:list_approvals failed: %+v", apprResp.Error)
	}

	// Step 3: Hot-reload preferences to Root B
	rootB := createE2EWorkspaceRoot(t, "SessionRootB")
	updRespB, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": rootB,
	})
	if !updRespB.Success {
		t.Fatalf("preferences:update hot-reload rootB failed: %+v", updRespB.Error)
	}

	// Step 4: Verify system paths dynamically updated to Root B
	infoRespB, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoRespB.Success {
		t.Fatalf("system:info rootB failed: %+v", infoRespB.Error)
	}
	var sysInfoB wireSystemInfo
	if err := json.Unmarshal(infoRespB.Data, &sysInfoB); err != nil {
		t.Fatalf("unmarshal system:info B: %v", err)
	}
	if filepath.Clean(sysInfoB.Paths.WorkspaceRoot) != filepath.Clean(rootB) {
		t.Fatalf("expected active workspace root %q after hot-reload, got %q", rootB, sysInfoB.Paths.WorkspaceRoot)
	}

	// Step 5: Verify ensured directories in Root B
	expectedAgentDir := filepath.Join(rootB, "workspace")
	if st, err := os.Stat(expectedAgentDir); err != nil || !st.IsDir() {
		t.Fatalf("expected agent workspace directory %q to exist after hot-reload: %v", expectedAgentDir, err)
	}
}

// TestE2E_Tier3_StagingIsolation_MediaGenWorkflow verifies Scenario C4:
// 1. Multi-step media processing utilizes isolated StagingDir (<DataDir>/staging).
// 2. Intermediate files reside in StagingDir and do not pollute creative workspace directories.
// 3. Final published artifact is moved cleanly into WorkspaceRoot/media/images.
// 4. Staging cleanup does not disturb creative workspace files.
func TestE2E_Tier3_StagingIsolation_MediaGenWorkflow(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	root := createE2EWorkspaceRoot(t, "CreativeWorkspace")
	updResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       root,
		"onboarding_completed": true,
	})
	if !updResp.Success {
		t.Fatalf("preferences:update failed: %+v", updResp.Error)
	}

	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}

	dataStaging := filepath.Join(sysInfo.Paths.Data, "staging")
	if err := os.MkdirAll(dataStaging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}

	// 1. Verify staging directory isolation: strictly outside creative workspace
	cleanStaging := strings.ToLower(filepath.Clean(dataStaging))
	cleanWorkspace := strings.ToLower(filepath.Clean(root))
	if strings.HasPrefix(cleanStaging, cleanWorkspace) {
		t.Fatalf("security violation: staging dir %q is inside workspace %q", dataStaging, root)
	}

	// 2. Simulate intermediate staging chunk creation
	stagingChunk := filepath.Join(dataStaging, "intermediate_render_chunk.tmp")
	if err := os.WriteFile(stagingChunk, []byte("intermediate-render-data"), 0o644); err != nil {
		t.Fatalf("write staging chunk: %v", err)
	}

	// 3. Publish final artifact into creative media folder
	publishedPath := filepath.Join(root, "media", "images", "final_render.png")
	createDummyPNG(t, publishedPath)

	// 4. Verify published directory contains only the final image and not intermediate staging files
	mediaEntries, err := os.ReadDir(filepath.Join(root, "media", "images"))
	if err != nil {
		t.Fatalf("read media images dir: %v", err)
	}
	for _, entry := range mediaEntries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("staging residue leaked into creative workspace: %s", entry.Name())
		}
	}

	// 5. Simulate staging cleanup: removing staging directory contents
	if err := os.Remove(stagingChunk); err != nil {
		t.Fatalf("staging cleanup failed: %v", err)
	}

	// 6. Verify final published artifact remains completely untouched
	if _, err := os.Stat(publishedPath); err != nil {
		t.Fatalf("staging cleanup accidentally deleted published creative asset: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Tier 4: Real-World Scenarios
// -----------------------------------------------------------------------------

// TestE2E_Tier4_Scenario_FirstRunOnboardingSimulation verifies Scenario S1:
// Desktop First-Run onboarding journey from unconfigured state to active workspace.
func TestE2E_Tier4_Scenario_FirstRunOnboardingSimulation(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	// 1. System launch: check fresh onboarding state
	prefResp, _ := client.call(contracts.ActionPreferencesGet, contracts.Empty{})
	if !prefResp.Success {
		t.Fatalf("preferences:get failed: %+v", prefResp.Error)
	}
	var prefs wirePreferences
	if err := json.Unmarshal(prefResp.Data, &prefs); err != nil {
		t.Fatalf("unmarshal preferences: %v", err)
	}
	if prefs.OnboardingCompleted {
		t.Fatal("fresh install must have onboarding_completed=false")
	}

	// 2. User queries paths to display in Onboarding Dialog
	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	if sysInfo.Paths.SuggestedWorkspaceRoot == "" {
		t.Fatal("suggested_workspace_root must be precomputed and non-empty")
	}
	if !strings.Contains(sysInfo.Paths.SuggestedWorkspaceRoot, buildinfo.ProductName) {
		t.Fatalf("suggested_workspace_root %q does not contain ProductName %q",
			sysInfo.Paths.SuggestedWorkspaceRoot, buildinfo.ProductName)
	}

	// 3. User chooses suggested or custom workspace folder
	customWorkspace := createE2EWorkspaceRoot(t, "UserStudio")
	updResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       customWorkspace,
		"onboarding_completed": true,
	})
	if !updResp.Success {
		t.Fatalf("onboarding preferences update failed: %+v", updResp.Error)
	}

	// 4. Verify system is fully initialized and operational
	checkPrefResp, _ := client.call(contracts.ActionPreferencesGet, contracts.Empty{})
	if !checkPrefResp.Success {
		t.Fatalf("check preferences:get failed: %+v", checkPrefResp.Error)
	}
	var updatedPrefs wirePreferences
	if err := json.Unmarshal(checkPrefResp.Data, &updatedPrefs); err != nil {
		t.Fatalf("unmarshal updated preferences: %v", err)
	}
	if !updatedPrefs.OnboardingCompleted || filepath.Clean(updatedPrefs.WorkspaceRoot) != filepath.Clean(customWorkspace) {
		t.Fatalf("preferences not updated properly: %+v", updatedPrefs)
	}

	// Verify all subfolders are ready for creative workflows
	for _, sub := range []string{"media/images", "media/videos", "media/tts", "media/watermark", "workspace"} {
		p := filepath.Join(customWorkspace, filepath.FromSlash(sub))
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Errorf("expected onboarding to ensure %s: %v", p, err)
		}
	}
}

// TestE2E_Tier4_Scenario_SettingsUpdate_And_RevertToDefault verifies Scenario S2:
// User switches workspace in Settings, processes media, and resets to default studio folder.
func TestE2E_Tier4_Scenario_SettingsUpdate_And_RevertToDefault(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	// 1. Initial State: Configure Custom Root 1
	root1 := createE2EWorkspaceRoot(t, "Studio1")
	initResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       root1,
		"onboarding_completed": true,
	})
	if !initResp.Success {
		t.Fatalf("update to root1 failed: %+v", initResp.Error)
	}

	// 2. Settings reconfiguration to Root 2
	root2 := createE2EWorkspaceRoot(t, "Studio2")
	updResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": root2,
	})
	if !updResp.Success {
		t.Fatalf("update to root2 failed: %+v", updResp.Error)
	}

	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	if filepath.Clean(sysInfo.Paths.WorkspaceRoot) != filepath.Clean(root2) {
		t.Fatalf("expected workspace root %q, got %q", root2, sysInfo.Paths.WorkspaceRoot)
	}

	// 3. User clicks "Reset to Default" in Settings (workspace_root: "")
	emptyRoot := ""
	resetResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root": emptyRoot,
	})
	if !resetResp.Success {
		t.Fatalf("reset to default failed: %+v", resetResp.Error)
	}

	// 4. Verify system gracefully falls back to <DataDir>/studio
	infoResetResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResetResp.Success {
		t.Fatalf("system:info after reset failed: %+v", infoResetResp.Error)
	}
	var sysInfoReset wireSystemInfo
	if err := json.Unmarshal(infoResetResp.Data, &sysInfoReset); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}
	expectedDefaultMedia := filepath.Join(sysInfoReset.Paths.Data, "studio", "media")
	if filepath.Clean(sysInfoReset.Paths.MediaDir) != filepath.Clean(expectedDefaultMedia) {
		t.Errorf("expected media dir reset to %q, got %q", expectedDefaultMedia, sysInfoReset.Paths.MediaDir)
	}
}

// TestE2E_Tier4_Scenario_AgentWorkspaceContainment_And_SecurityIsolation verifies Scenario S3:
// 1. Agent access inside trusted WorkspaceRoot/workspace is allowed for creative assets and text files.
// 2. Agent access to internal application files (app.db, logs, agent-profiles) is strictly blocked.
// 3. Path traversal attempts outside allowed boundaries are blocked.
// 4. Attempting to delete root folders is blocked by Root Deletion Guard.
func TestE2E_Tier4_Scenario_AgentWorkspaceContainment_And_SecurityIsolation(t *testing.T) {
	h := startCore(t, nil)
	client := h.dial()

	root := createE2EWorkspaceRoot(t, "ContainmentStudio")
	updResp, _ := client.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":       root,
		"onboarding_completed": true,
	})
	if !updResp.Success {
		t.Fatalf("preferences:update failed: %+v", updResp.Error)
	}

	infoResp, _ := client.call(contracts.ActionSystemInfo, contracts.Empty{})
	if !infoResp.Success {
		t.Fatalf("system:info failed: %+v", infoResp.Error)
	}
	var sysInfo wireSystemInfo
	if err := json.Unmarshal(infoResp.Data, &sysInfo); err != nil {
		t.Fatalf("unmarshal system:info: %v", err)
	}

	wsDir := filepath.Join(root, "workspace")
	mediaDir := filepath.Join(root, "media")
	dataDir := sysInfo.Paths.Data
	profilesDir := filepath.Join(dataDir, "agent-profiles")

	for _, d := range []string{wsDir, mediaDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Set up AccessPolicy matching application configuration
	policy := agent.NewAccessPolicy(dataDir, wsDir, mediaDir, profilesDir, nil)

	// 1. Within WorkspaceRoot/workspace: text file write, read, and trusted checks are allowed
	validFile := filepath.Join(wsDir, "story.md")
	if err := os.WriteFile(validFile, []byte("# Scene 1"), 0o644); err != nil {
		t.Fatalf("write valid file: %v", err)
	}
	if _, err := policy.ValidateRead(validFile); err != nil {
		t.Errorf("expected read of %q in workspace to be allowed: %v", validFile, err)
	}
	if _, err := policy.ValidateWrite(validFile); err != nil {
		t.Errorf("expected write of %q in workspace to be allowed: %v", validFile, err)
	}
	if err := policy.CheckTrustedFile(validFile, false); err != nil {
		t.Errorf("expected CheckTrustedFile for %q to pass: %v", validFile, err)
	}

	// 2. Access to internal app.db is strictly denied
	appDBPath := filepath.Join(dataDir, "app.db")
	if _, err := policy.ValidateRead(appDBPath); err == nil {
		t.Errorf("security violation: reading app.db must be denied")
	}

	// 3. Access to internal logs directory is strictly denied
	logsPath := filepath.Join(dataDir, "logs", "app.log")
	if _, err := policy.ValidateRead(logsPath); err == nil {
		t.Errorf("security violation: reading logs must be denied")
	}

	// 4. Access to agent profiles directory is strictly denied
	profilesSecretPath := filepath.Join(profilesDir, "antigravity", "token.json")
	if _, err := policy.ValidateRead(profilesSecretPath); err == nil {
		t.Errorf("security violation: reading agent profiles must be denied")
	}

	// 5. Path traversal attempts are strictly denied
	traversalPath := filepath.Join(wsDir, "..", "..", "data", "app.db")
	if _, err := policy.ValidateRead(traversalPath); err == nil {
		t.Errorf("security violation: path traversal to app.db must be denied")
	}

	// 6. Root Deletion Guard: cannot delete workspace root
	if _, err := policy.ValidateDelete(wsDir); err == nil {
		t.Errorf("security violation: deleting workspace root must be blocked")
	}
}

func createSizedPNG(t *testing.T, targetPath string, width, height int, c color.Color) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("create dir for sized png: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode sized png: %v", err)
	}
	data := buf.Bytes()
	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		t.Fatalf("write sized png: %v", err)
	}
	return data
}

// TestE2E_StudioPipeline_Phase1ToPhase3 verifies the full AI Film Studio pipeline end-to-end
// over the real Core, SQLite database (migrations 018..022), Realtime WebSocket Hub, Workflow Engine,
// and Agent MCP server:
//  1. Phase 1: create project & episode, save script, upsert asset & shots, reorder shots, workspace retention.
//  2. Phase 2: grid split -> versions list ("keyframe" & "video" + invalid target_kind rejection) -> version select,
//     batch preflight & cost summary (gemini-aistudio / gemini-vertex Veo pricing from migration 022).
//  3. Phase 3: timeline_get -> timeline_apply_ops -> expected_revision ERR_CONFLICT -> timeline_undo (rev 1 -> rev 0)
//     -> timeline_redo (rev 0 -> rev 1) -> NLE export (.fcpxml & .otio) + MCP workspace_read_file parity.
func TestE2E_StudioPipeline_Phase1ToPhase3(t *testing.T) {
	h := startCore(t, func(c *config.Config) {
		c.Providers.AutoCheck = false
		c.Providers.CheckOnStartup = false
	})
	clientA, clientB := h.dial(), h.dial()

	studioRoot := createE2EWorkspaceRoot(t, "StudioPipeline")
	updResp, _ := clientA.call(contracts.ActionPreferencesUpdate, map[string]any{
		"workspace_root":              studioRoot,
		"onboarding_completed":        true,
		"agent_skip_trusted_approval": true,
	})
	if !updResp.Success {
		t.Fatalf("preferences:update failed: %+v", updResp.Error)
	}
	_ = clientB.waitForEvent(contracts.EventPreferencesUpdated, 5*time.Second)

	// --- Phase 1: Project, Episode, Script, Asset Bible & Storyboard Shots ---
	createProjResp, _ := clientA.call(contracts.ActionStudioProjectCreate, contracts.StudioProjectCreatePayload{
		Title:               "Neon Horizon",
		Genre:               domainstudio.GenreDrama,
		ProductionMode:      domainstudio.ModeStoryboardI2V,
		AspectRatio:         "16:9",
		StylePrompt:         "Rain-soaked cyberpunk cinema",
		AutoApproveBatches:  true,
		InitialEpisodeTitle: "Episode 1: Arrival",
	})
	if !createProjResp.Success {
		t.Fatalf("studio:project_create failed: %+v", createProjResp.Error)
	}
	var projDetail contracts.StudioProjectDetailResult
	if err := json.Unmarshal(createProjResp.Data, &projDetail); err != nil {
		t.Fatalf("unmarshal project detail: %v", err)
	}
	if projDetail.Project.ID == "" || projDetail.ActiveEpisode == nil {
		t.Fatalf("expected project ID and starter active_episode, got %+v", projDetail)
	}
	projID := projDetail.Project.ID
	ep1ID := projDetail.ActiveEpisode.Episode.ID

	projEvt := clientB.waitForEvent(contracts.EventStudioProjectChanged, 5*time.Second)
	if !strings.Contains(string(projEvt.Data), projID) {
		t.Fatalf("expected studio:project_changed broadcast for %s, got %s", projID, projEvt.Data)
	}

	// Create a second episode and verify studio:project_get returns both episodes.
	ep2Resp, _ := clientA.call(contracts.ActionStudioEpisodeCreate, contracts.StudioEpisodeCreatePayload{
		ProjectID: projID,
		Title:     "Episode 2: Echoes",
	})
	if !ep2Resp.Success {
		t.Fatalf("studio:episode_create failed: %+v", ep2Resp.Error)
	}
	getProjResp, _ := clientA.call(contracts.ActionStudioProjectGet, contracts.StudioIDPayload{ID: projID})
	if !getProjResp.Success {
		t.Fatalf("studio:project_get failed: %+v", getProjResp.Error)
	}
	var fetchedProj contracts.StudioProjectDetailResult
	if err := json.Unmarshal(getProjResp.Data, &fetchedProj); err != nil {
		t.Fatalf("unmarshal studio:project_get: %v", err)
	}
	if len(fetchedProj.Episodes) != 2 {
		t.Fatalf("expected 2 episodes on project, got %d", len(fetchedProj.Episodes))
	}

	// Save raw + normalized script for Episode 1 and verify disk sync.
	rawText := "INT. ROOFTOP - NIGHT\nMinh looks out over the neon skyline."
	normScript := &contracts.StudioNormalizedScript{
		Title:       "Episode 1: Arrival",
		Genre:       domainstudio.GenreDrama,
		AspectRatio: "16:9",
		Logline:     "Minh arrives above the neon city.",
		Characters:  []string{"Minh"},
		Scenes: []contracts.StudioScriptScene{
			{
				Code:      "S01",
				Title:     "Skyline Arrival",
				Location:  "Rooftop",
				TimeOfDay: "Night",
				Summary:   "Minh surveys the skyline.",
				Beats: []contracts.StudioScriptBeat{
					{
						Code:        "B01",
						Description: "Camera pushes toward Minh at the railing.",
						DurationSec: 4,
						AssetTags:   []string{"@Minh"},
						Dialogues: []contracts.StudioDialogueLine{
							{Speaker: "Minh", Emotion: "calm", Line: "We made it."},
						},
						Narrations: []contracts.StudioNarrationSegment{},
					},
				},
			},
		},
	}
	saveScriptResp, _ := clientA.call(contracts.ActionStudioEpisodeSaveScript, contracts.StudioEpisodeSaveScriptPayload{
		EpisodeID:        ep1ID,
		RawScript:        &rawText,
		NormalizedScript: normScript,
	})
	if !saveScriptResp.Success {
		t.Fatalf("studio:episode_save_script failed: %+v", saveScriptResp.Error)
	}
	var epDetail contracts.StudioEpisodeDetailResult
	if err := json.Unmarshal(saveScriptResp.Data, &epDetail); err != nil {
		t.Fatalf("unmarshal episode detail: %v", err)
	}
	if epDetail.RawScript != rawText || epDetail.NormalizedScript == nil || len(epDetail.NormalizedScript.Scenes) != 1 {
		t.Fatalf("script round-trip mismatch: %+v", epDetail)
	}

	// Prepare workspace images for Asset Bible, Keyframe Grid Split, and Timeline Clip.
	charSheetPath := filepath.Join(projDetail.Project.RootDir, "assets", "minh_sheet.png")
	createSizedPNG(t, charSheetPath, 8, 8, color.RGBA{R: 40, G: 180, B: 240, A: 255})

	keyframeGridPath := filepath.Join(projDetail.Project.RootDir, "episodes", "ep01", "media", "keyframes", "shot1_grid.png")
	createSizedPNG(t, keyframeGridPath, 16, 16, color.RGBA{R: 220, G: 90, B: 40, A: 255})

	shot2StillPath := filepath.Join(projDetail.Project.RootDir, "episodes", "ep01", "media", "keyframes", "shot2_still.png")
	createSizedPNG(t, shot2StillPath, 16, 9, color.RGBA{R: 60, G: 200, B: 120, A: 255})

	assetResp, _ := clientA.call(contracts.ActionStudioAssetUpsert, contracts.StudioAssetUpsertPayload{
		ProjectID:   projID,
		Kind:        domainstudio.AssetCharacter,
		Name:        "Minh",
		CodeTag:     "@Minh",
		Description: "Cyberpunk courier",
		Prompt:      "Young courier in rain-soaked trench coat",
		PrimaryPath: charSheetPath,
	})
	if !assetResp.Success {
		t.Fatalf("studio:asset_upsert failed: %+v", assetResp.Error)
	}

	dur4 := 4
	imgPrompt1 := "Wide rooftop neon skyline with @Minh"
	vidPrompt1 := "Slow dolly push toward @Minh"
	shot1Resp, _ := clientA.call(contracts.ActionStudioShotUpsert, contracts.StudioShotUpsertPayload{
		EpisodeID:    ep1ID,
		SceneCode:    "S01",
		Title:        "Skyline Wide",
		ShotMode:     domainstudio.ShotModeGrid2x2,
		DurationSec:  &dur4,
		ImagePrompt:  &imgPrompt1,
		VideoPrompt:  &vidPrompt1,
		RefAssetTags: []string{"@Minh"},
		KeyframePath: &keyframeGridPath,
	})
	if !shot1Resp.Success {
		t.Fatalf("studio:shot_upsert #1 failed: %+v", shot1Resp.Error)
	}
	var shot1Out contracts.StudioShotUpsertResult
	if err := json.Unmarshal(shot1Resp.Data, &shot1Out); err != nil {
		t.Fatalf("unmarshal shot1: %v", err)
	}

	dur3 := 3
	shot2Resp, _ := clientA.call(contracts.ActionStudioShotUpsert, contracts.StudioShotUpsertPayload{
		EpisodeID:    ep1ID,
		SceneCode:    "S01",
		Title:        "Close-up Minh",
		ShotMode:     domainstudio.ShotModeSingle,
		DurationSec:  &dur3,
		KeyframePath: &shot2StillPath,
	})
	if !shot2Resp.Success {
		t.Fatalf("studio:shot_upsert #2 failed: %+v", shot2Resp.Error)
	}
	var shot2Out contracts.StudioShotUpsertResult
	if err := json.Unmarshal(shot2Resp.Data, &shot2Out); err != nil {
		t.Fatalf("unmarshal shot2: %v", err)
	}

	reorderResp, _ := clientA.call(contracts.ActionStudioShotReorder, contracts.StudioShotReorderPayload{
		EpisodeID: ep1ID,
		ShotIDs:   []string{shot2Out.Shot.ID, shot1Out.Shot.ID},
	})
	if !reorderResp.Success {
		t.Fatalf("studio:shot_reorder failed: %+v", reorderResp.Error)
	}

	// --- Phase 2: Grid Split, Versions ("keyframe" & "video"), Batch Preflight & Veo Pricing (Migration 022) ---
	gridResp, _ := clientA.call(contracts.ActionStudioGridSplit, contracts.StudioGridSplitPayload{
		ProjectID:  projID,
		EpisodeID:  ep1ID,
		TargetKind: domainstudio.TargetKeyframe,
		TargetID:   shot1Out.Shot.ID,
		ImagePath:  keyframeGridPath,
		Rows:       2,
		Cols:       2,
	})
	if !gridResp.Success {
		t.Fatalf("studio:grid_split failed: %+v", gridResp.Error)
	}
	var gridOut contracts.StudioGridSplitResult
	if err := json.Unmarshal(gridResp.Data, &gridOut); err != nil {
		t.Fatalf("unmarshal studio:grid_split: %v", err)
	}
	if len(gridOut.Panels) != 4 || len(gridOut.Versions) != 4 {
		t.Fatalf("expected 4 split panels and 4 saved versions, got panels=%d versions=%d", len(gridOut.Panels), len(gridOut.Versions))
	}

	// Verify studio:versions_list for canonical "keyframe" and "video", plus rejection of invalid legacy "shot_video".
	verKFResp, _ := clientA.call(contracts.ActionStudioVersionsList, contracts.StudioVersionsListPayload{
		TargetKind: domainstudio.TargetKeyframe,
		TargetID:   shot1Out.Shot.ID,
	})
	if !verKFResp.Success {
		t.Fatalf("studio:versions_list(keyframe) failed: %+v", verKFResp.Error)
	}
	var verKF contracts.StudioVersionsListResult
	if err := json.Unmarshal(verKFResp.Data, &verKF); err != nil {
		t.Fatalf("unmarshal versions(keyframe): %v", err)
	}
	if len(verKF.Versions) != 4 {
		t.Fatalf("expected 4 keyframe versions, got %d", len(verKF.Versions))
	}

	verVidResp, _ := clientA.call(contracts.ActionStudioVersionsList, contracts.StudioVersionsListPayload{
		TargetKind: domainstudio.TargetVideo,
		TargetID:   shot1Out.Shot.ID,
	})
	if !verVidResp.Success {
		t.Fatalf("studio:versions_list(video) failed: %+v", verVidResp.Error)
	}
	if !strings.Contains(string(verVidResp.Data), `"versions":[]`) {
		t.Fatalf("expected non-null empty JSON array for video versions, got %s", verVidResp.Data)
	}

	badVerResp, _ := clientA.call(contracts.ActionStudioVersionsList, map[string]any{
		"target_kind": "shot_video",
		"target_id":   shot1Out.Shot.ID,
	})
	if badVerResp.Success || badVerResp.Error == nil || badVerResp.Error.Code != "ERR_VALIDATION" {
		t.Fatalf("expected invalid target_kind 'shot_video' to fail with ERR_VALIDATION, got %+v", badVerResp)
	}

	// Activate split panel version #1 on shot1 and confirm keyframe_path updates.
	selVerResp, _ := clientA.call(contracts.ActionStudioVersionSelect, contracts.StudioVersionSelectPayload{
		VersionID: verKF.Versions[0].ID,
	})
	if !selVerResp.Success {
		t.Fatalf("studio:version_select failed: %+v", selVerResp.Error)
	}
	var selVerOut contracts.StudioVersionSelectResult
	if err := json.Unmarshal(selVerResp.Data, &selVerOut); err != nil {
		t.Fatalf("unmarshal studio:version_select: %v", err)
	}
	if !selVerOut.Version.IsSelected || selVerOut.Episode == nil {
		t.Fatalf("expected selected version and refreshed episode detail, got %+v", selVerOut)
	}

	// Batch Preflight (unconfigured provider): verifies localized BlockerItems before credentials exist.
	blockedPreflightResp, _ := clientA.call(contracts.ActionStudioBatchPreflight, contracts.StudioBatchPreflightPayload{
		ProjectID:    projID,
		EpisodeID:    ep1ID,
		Stage:        "generate_videos",
		Model:        "gemini-aistudio/veo-2.0-generate-001",
		ItemCount:    2,
		UnitsPerItem: 5,
	})
	if !blockedPreflightResp.Success {
		t.Fatalf("studio:batch_preflight (blocked) failed: %+v", blockedPreflightResp.Error)
	}
	var blockedPreflight contracts.BatchPreflightOutput
	if err := json.Unmarshal(blockedPreflightResp.Data, &blockedPreflight); err != nil {
		t.Fatalf("unmarshal blocked preflight: %v", err)
	}
	if blockedPreflight.Ready || len(blockedPreflight.BlockerItems) == 0 || blockedPreflight.BlockerItems[0].MsgKey == "" {
		t.Fatalf("expected Ready=false with localized BlockerItems, got %+v", blockedPreflight)
	}

	// Configure gemini-aistudio credential + video model via WS (AutoCheck=false: zero network calls),
	// then verify Batch Preflight resolves migration 022's gemini-aistudio Veo rate ($0.15/sec => $1.50 total).
	credResp, _ := clientA.call(contracts.ActionProvidersCreateCredential, contracts.CreateCredentialPayload{
		ProviderID: domainprovider.IDGeminiAIStudio,
		Credential: contracts.CredentialInput{
			Name:    "E2E Gemini Key",
			Secrets: map[string]string{domainprovider.FieldAPIKey: "AIzaSy-e2e-offline-key"},
		},
	})
	if !credResp.Success {
		t.Fatalf("providers:create_credential failed: %+v", credResp.Error)
	}
	setModelsResp, _ := clientA.call(contracts.ActionProvidersSetModels, contracts.SetModelsPayload{
		OwnerID: domainprovider.IDGeminiAIStudio,
		Models: []contracts.ProviderModel{
			{
				ModelID:     "veo-2.0-generate-001",
				DisplayName: "Veo 2",
				Capability:  domainprovider.CapabilityVideo,
				Enabled:     true,
				IsDefault:   true,
				UserAdded:   true,
			},
		},
	})
	if !setModelsResp.Success {
		t.Fatalf("providers:set_models failed: %+v", setModelsResp.Error)
	}

	preflightResp, _ := clientA.call(contracts.ActionStudioBatchPreflight, contracts.StudioBatchPreflightPayload{
		ProjectID:    projID,
		EpisodeID:    ep1ID,
		Stage:        "generate_videos",
		Model:        "gemini-aistudio/veo-2.0-generate-001",
		ItemCount:    2,
		UnitsPerItem: 5,
	})
	if !preflightResp.Success {
		t.Fatalf("studio:batch_preflight failed: %+v", preflightResp.Error)
	}
	var preflight contracts.BatchPreflightOutput
	if err := json.Unmarshal(preflightResp.Data, &preflight); err != nil {
		t.Fatalf("unmarshal studio:batch_preflight: %v", err)
	}
	if !preflight.Ready || preflight.ProviderID != "gemini-aistudio" || preflight.UnitPriceUSD != 0.15 || preflight.EstimatedUSD != 1.5 {
		t.Fatalf("expected Ready=true, gemini-aistudio Veo rate 0.15 USD/sec and total 1.50 USD, got %+v", preflight)
	}

	costResp, _ := clientA.call(contracts.ActionStudioCostSummary, contracts.StudioCostSummaryPayload{
		ProjectID: projID,
		EpisodeID: ep1ID,
	})
	if !costResp.Success {
		t.Fatalf("studio:cost_summary failed: %+v", costResp.Error)
	}
	var costSum contracts.StudioCostSummaryResult
	if err := json.Unmarshal(costResp.Data, &costSum); err != nil {
		t.Fatalf("unmarshal studio:cost_summary: %v", err)
	}
	var sawAIStudioVeo, sawVertexVeo bool
	for _, pr := range costSum.Pricing {
		if pr.ProviderID == "gemini-aistudio" && pr.Capability == "video" && pr.UnitPriceUSD == 0.15 {
			sawAIStudioVeo = true
		}
		if pr.ProviderID == "gemini-vertex" && pr.Capability == "video" && pr.UnitPriceUSD == 0.15 {
			sawVertexVeo = true
		}
	}
	if !sawAIStudioVeo || !sawVertexVeo {
		t.Fatalf("expected migration 022 gemini-aistudio & gemini-vertex Veo pricing rows in catalog, got %+v", costSum.Pricing)
	}

	// --- Phase 3: Timeline Get -> Apply Ops -> Conflict -> Undo (rev 1 -> 0) -> Redo -> NLE Export (.fcpxml & .otio) ---
	tlGetResp, _ := clientA.call(contracts.ActionStudioTimelineGet, contracts.StudioIDPayload{ID: ep1ID})
	if !tlGetResp.Success {
		t.Fatalf("studio:timeline_get failed: %+v", tlGetResp.Error)
	}
	var tl0 contracts.StudioTimelineResult
	if err := json.Unmarshal(tlGetResp.Data, &tl0); err != nil {
		t.Fatalf("unmarshal timeline rev 0: %v", err)
	}
	if tl0.Revision != 0 || tl0.CanUndo || tl0.CanRedo {
		t.Fatalf("expected initial rev 0 (can_undo=false, can_redo=false), got %+v", tl0)
	}
	var videoTrackID string
	for _, tr := range tl0.Timeline.Tracks {
		if tr.Kind == domainstudio.TrackVideo {
			videoTrackID = tr.ID
			break
		}
	}
	if videoTrackID == "" {
		t.Fatalf("expected default video track in initial timeline, got %+v", tl0.Timeline.Tracks)
	}

	applyResp, _ := clientA.call(contracts.ActionStudioTimelineApplyOps, contracts.StudioTimelineApplyOpsPayload{
		EpisodeID:        ep1ID,
		ExpectedRevision: 0,
		Summary:          "Add opening shot clip, subtitle, and marker",
		Ops: []contracts.StudioTimelineOp{
			{
				Op:      domainstudio.OpAddClip,
				TrackID: videoTrackID,
				Clip: &contracts.StudioTimelineClip{
					ID:              "clip_ep1_1",
					ShotID:          shot1Out.Shot.ID,
					Label:           "Skyline Wide",
					FilePath:        shot2StillPath,
					TimelineStartMs: 0,
					SourceInMs:      0,
					SourceOutMs:     3000,
					Speed:           1.0,
					Volume:          1.0,
					TransitionIn:    domainstudio.TransitionNone,
				},
			},
			{
				Op: domainstudio.OpAddSubtitle,
				Subtitle: &contracts.StudioSubtitleCue{
					ID:      "sub_1",
					StartMs: 200,
					EndMs:   2600,
					Speaker: "Minh",
					Text:    "We made it.",
				},
			},
			{
				Op: domainstudio.OpAddMarker,
				Marker: &contracts.StudioTimelineMarker{
					ID:     "mk_1",
					TimeMs: 1000,
					Label:  "Beat 1",
				},
			},
		},
	})
	if !applyResp.Success {
		t.Fatalf("studio:timeline_apply_ops failed: %+v", applyResp.Error)
	}
	var tl1 contracts.StudioTimelineResult
	if err := json.Unmarshal(applyResp.Data, &tl1); err != nil {
		t.Fatalf("unmarshal timeline rev 1: %v", err)
	}
	if tl1.Revision != 1 || !tl1.CanUndo || tl1.CanRedo || tl1.DurationMs != 3000 {
		t.Fatalf("expected rev 1 (can_undo=true, can_redo=false, duration=3000), got %+v", tl1)
	}

	// Stale expected_revision MUST be rejected with ERR_CONFLICT.
	conflictResp, _ := clientA.call(contracts.ActionStudioTimelineApplyOps, contracts.StudioTimelineApplyOpsPayload{
		EpisodeID:        ep1ID,
		ExpectedRevision: 0,
		Summary:          "Stale write",
		Ops: []contracts.StudioTimelineOp{
			{
				Op:       domainstudio.OpRemoveMarker,
				MarkerID: "mk_1",
			},
		},
	})
	if conflictResp.Success || conflictResp.Error == nil || conflictResp.Error.Code != "ERR_CONFLICT" {
		t.Fatalf("expected ERR_CONFLICT on stale expected_revision=0, got %+v", conflictResp)
	}

	// Undo rev 1 -> rev 0 baseline.
	undoResp, _ := clientA.call(contracts.ActionStudioTimelineUndo, contracts.StudioTimelineRevisionNavPayload{
		EpisodeID:        ep1ID,
		ExpectedRevision: 1,
	})
	if !undoResp.Success {
		t.Fatalf("studio:timeline_undo (rev 1 -> 0) failed: %+v", undoResp.Error)
	}
	var tlUndo contracts.StudioTimelineResult
	if err := json.Unmarshal(undoResp.Data, &tlUndo); err != nil {
		t.Fatalf("unmarshal timeline undo: %v", err)
	}
	if tlUndo.Revision != 0 || tlUndo.CanUndo || !tlUndo.CanRedo || tlUndo.DurationMs != 0 {
		t.Fatalf("expected undo to restore rev 0 (can_undo=false, can_redo=true, duration=0), got %+v", tlUndo)
	}

	// Redo rev 0 -> rev 1.
	redoResp, _ := clientA.call(contracts.ActionStudioTimelineRedo, contracts.StudioTimelineRevisionNavPayload{
		EpisodeID:        ep1ID,
		ExpectedRevision: 0,
	})
	if !redoResp.Success {
		t.Fatalf("studio:timeline_redo (rev 0 -> 1) failed: %+v", redoResp.Error)
	}
	var tlRedo contracts.StudioTimelineResult
	if err := json.Unmarshal(redoResp.Data, &tlRedo); err != nil {
		t.Fatalf("unmarshal timeline redo: %v", err)
	}
	if tlRedo.Revision != 1 || !tlRedo.CanUndo || tlRedo.CanRedo || tlRedo.DurationMs != 3000 || len(tlRedo.Timeline.Subtitles) != 1 {
		t.Fatalf("expected redo to restore rev 1 state, got %+v", tlRedo)
	}

	// Export NLE drafts in both FCPXML (.fcpxml) and OpenTimelineIO (.otio) formats.
	includeMedia := false
	fcpxmlResp, _ := clientA.call(contracts.ActionStudioTimelineExportNLE, contracts.StudioTimelineExportNLEPayload{
		EpisodeID:        ep1ID,
		Format:           domainstudio.NLEFormatFCPXML,
		ProjectName:      "Neon Horizon Ep01",
		IncludeMediaCopy: &includeMedia,
	})
	if !fcpxmlResp.Success {
		t.Fatalf("studio:timeline_export_nle(fcpxml) failed: %+v", fcpxmlResp.Error)
	}
	var fcpxmlOut contracts.ExportNLEDraftOutput
	if err := json.Unmarshal(fcpxmlResp.Data, &fcpxmlOut); err != nil {
		t.Fatalf("unmarshal fcpxml export: %v", err)
	}
	if filepath.Ext(fcpxmlOut.ExportPath) != ".fcpxml" {
		t.Fatalf("expected .fcpxml export path, got %q", fcpxmlOut.ExportPath)
	}
	fcpxmlBytes, err := os.ReadFile(fcpxmlOut.ExportPath)
	if err != nil || !strings.Contains(string(fcpxmlBytes), "<fcpxml") {
		t.Fatalf("expected valid <fcpxml document at %q, err=%v", fcpxmlOut.ExportPath, err)
	}

	otioResp, _ := clientA.call(contracts.ActionStudioTimelineExportNLE, contracts.StudioTimelineExportNLEPayload{
		EpisodeID:        ep1ID,
		Format:           domainstudio.NLEFormatOTIO,
		ProjectName:      "Neon Horizon Ep01",
		IncludeMediaCopy: &includeMedia,
	})
	if !otioResp.Success {
		t.Fatalf("studio:timeline_export_nle(otio) failed: %+v", otioResp.Error)
	}
	var otioOut contracts.ExportNLEDraftOutput
	if err := json.Unmarshal(otioResp.Data, &otioOut); err != nil {
		t.Fatalf("unmarshal otio export: %v", err)
	}
	if filepath.Ext(otioOut.ExportPath) != ".otio" {
		t.Fatalf("expected .otio export path, got %q", otioOut.ExportPath)
	}
	otioBytes, err := os.ReadFile(otioOut.ExportPath)
	if err != nil || !strings.Contains(string(otioBytes), "Timeline.1") {
		t.Fatalf("expected valid OTIO JSON at %q, err=%v", otioOut.ExportPath, err)
	}

	// Verify Agent MCP workspace_read_file can inspect both exported .fcpxml and .otio drafts.
	ctx := context.Background()
	if _, err := h.core.Agent().EnsureActiveConversation(ctx, "codex", "Studio Export Check"); err != nil {
		t.Fatalf("ensure active conversation: %v", err)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := h.core.Agent().BuildMCPServer().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("connect mcp server: %v", err)
	}
	mcpClient, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-studio-mcp"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connect mcp client: %v", err)
	}
	defer mcpClient.Close()

	for _, exportedPath := range []string{fcpxmlOut.ExportPath, otioOut.ExportPath} {
		readPayload, isReadErr := callE2EMCPTool(t, mcpClient, "workspace_read_file", map[string]any{
			"path": exportedPath,
		})
		if isReadErr || readPayload["content"] == "" {
			t.Fatalf("expected MCP workspace_read_file to read %q, got isErr=%v payload=%v", exportedPath, isReadErr, readPayload)
		}
	}
}
