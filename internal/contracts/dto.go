package contracts

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
	"app/internal/domain/agent"
	"app/internal/domain/binaries"
	"app/internal/domain/preferences"
	"app/internal/engine"
)

// Empty is the payload/result of actions that carry no data.
type Empty struct{}

// PingResult answers system:ping.
type PingResult struct {
	ServerTime int64 `json:"server_time"`
}

// BuildInfo describes the running binary.
type BuildInfo struct {
	AppID       string `json:"app_id"`
	ProductName string `json:"product_name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildDate   string `json:"build_date"`
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Dev         bool   `json:"dev"`
}

// NewBuildInfo converts buildinfo.Info.
func NewBuildInfo(i buildinfo.Info) BuildInfo { return BuildInfo(i) }

// RuntimeInfo holds process metrics.
type RuntimeInfo struct {
	NumCPU        int     `json:"num_cpu"`
	NumGoroutine  int     `json:"num_goroutine"`
	AllocMB       float64 `json:"alloc_mb"`
	SysMB         float64 `json:"sys_mb"`
	GCCycles      uint32  `json:"gc_cycles"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

// PathsInfo lists the application directories.
type PathsInfo struct {
	Config      string `json:"config"`
	Data        string `json:"data"`
	Logs        string `json:"logs"`
	Diagnostics string `json:"diagnostics"`
}

// SystemInfo answers system:info.
type SystemInfo struct {
	Mode    config.Mode `json:"mode"`
	Build   BuildInfo   `json:"build"`
	Runtime RuntimeInfo `json:"runtime"`
	Paths   PathsInfo   `json:"paths"`
}

// FolderTarget names a folder that system:open_folder may reveal.
type FolderTarget string

const (
	FolderLogs        FolderTarget = "logs"
	FolderData        FolderTarget = "data"
	FolderConfig      FolderTarget = "config"
	FolderDiagnostics FolderTarget = "diagnostics"
)

// FolderTargets lists valid targets.
func FolderTargets() []FolderTarget {
	return []FolderTarget{FolderLogs, FolderData, FolderConfig, FolderDiagnostics}
}

// OpenFolderPayload is the payload of system:open_folder.
type OpenFolderPayload struct {
	Target FolderTarget `json:"target"`
}

// Validate implements payload validation.
func (p OpenFolderPayload) Validate() error {
	if !slices.Contains(FolderTargets(), p.Target) {
		return apperr.Validation(fmt.Sprintf("unknown folder target %q", p.Target))
	}
	return nil
}

// UpdateInfo answers system:check_update.
type UpdateInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Available      bool   `json:"available"`
	ReleaseURL     string `json:"release_url"`
	ReleaseNotes   string `json:"release_notes"`
	CheckedAt      int64  `json:"checked_at"`
}

// Preferences mirrors the domain preference document.
type Preferences struct {
	Theme    preferences.Theme    `json:"theme"`
	Language preferences.Language `json:"language"`
}

// NewPreferences converts the domain model.
func NewPreferences(p preferences.Preferences) Preferences {
	return Preferences{
		Theme:    p.Theme,
		Language: p.Language,
	}
}

// PreferencesPatch is the payload of preferences:update; omitted fields are unchanged.
type PreferencesPatch struct {
	Theme    *preferences.Theme    `json:"theme,omitempty"`
	Language *preferences.Language `json:"language,omitempty"`
}

// Validate rejects empty patches.
func (p PreferencesPatch) Validate() error {
	if p.Theme == nil && p.Language == nil {
		return apperr.Validation("patch must change at least one field")
	}
	return nil
}

// ToDomain converts to the domain patch.
func (p PreferencesPatch) ToDomain() preferences.Patch {
	return preferences.Patch{
		Theme:    p.Theme,
		Language: p.Language,
	}
}

// DiagnosticsBundle answers diagnostics:export.
type DiagnosticsBundle struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt int64  `json:"created_at"`
}

// ActionProgress is the data of action:progress.
type ActionProgress struct {
	Percent float64 `json:"percent"` // 0-100
	Message string  `json:"message"`
}

// ConnectionInfo tells the frontend how to reach the core (desktop bridge).
type ConnectionInfo struct {
	Mode                  config.Mode `json:"mode"`
	WSURL                 string      `json:"ws_url"`
	Token                 string      `json:"token"`
	ActionTimeoutMs       int64       `json:"action_timeout_ms"`
	LongActionTimeoutMs   int64       `json:"long_action_timeout_ms"`
	PingIntervalMs        int64       `json:"ping_interval_ms"`
	CheckUpdatesOnStartup bool        `json:"check_updates_on_startup"`
}

// BinaryInfo represents an external binary status.
type BinaryInfo struct {
	ID           binaries.BinaryID `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Executable   string            `json:"executable"`
	ResolvedPath string            `json:"resolved_path"`
	CustomPath   string            `json:"custom_path"`
	Source       binaries.Source   `json:"source"`
	Version      string            `json:"version"`
	Status       binaries.Status   `json:"status"`
	CanDownload  bool              `json:"can_download"`
}

// BinaryListResult answers binaries:list.
type BinaryListResult struct {
	Binaries []BinaryInfo `json:"binaries"`
}

// BinaryIDPayload targets a specific binary.
type BinaryIDPayload struct {
	ID binaries.BinaryID `json:"id"`
}

// Validate checks binary id.
func (p BinaryIDPayload) Validate() error {
	return binaries.ValidateID(p.ID)
}

// BinarySetPathPayload sets a custom executable path.
type BinarySetPathPayload struct {
	ID         binaries.BinaryID `json:"id"`
	CustomPath string            `json:"custom_path"`
}

// Validate checks payload.
func (p BinarySetPathPayload) Validate() error {
	return binaries.ValidateID(p.ID)
}

// WorkflowSpecsResult returns available node specs.
type WorkflowSpecsResult struct {
	Specs []engine.Spec `json:"specs"`
}

// WorkflowRunNodePayload executes a single node by type.
type WorkflowRunNodePayload struct {
	Type  string          `json:"type"`
	Input json.RawMessage `json:"input"`
}

// Validate checks node payload.
func (p WorkflowRunNodePayload) Validate() error {
	if p.Type == "" {
		return apperr.Validation("node type is required")
	}
	return nil
}

// WorkflowRunNodeResult wraps single node execution output.
type WorkflowRunNodeResult struct {
	Output any `json:"output"`
}

// WorkflowRunGraphPayload wraps a DAG graph to execute.
type WorkflowRunGraphPayload struct {
	Graph engine.Graph `json:"graph"`
}

// Validate checks graph payload.
func (p WorkflowRunGraphPayload) Validate() error {
	if len(p.Graph.Nodes) == 0 {
		return apperr.Validation("graph must contain at least one node")
	}
	return nil
}

// WorkflowRunGraphResult returns full graph execution results.
type WorkflowRunGraphResult struct {
	Result engine.GraphResult `json:"result"`
}

// AgentProviderInfo describes an external isolated agent provider and its live connection state.
type AgentProviderInfo struct {
	ID                 agent.ProviderID      `json:"id"`
	Name               string                `json:"name"`
	Description        string                `json:"description"`
	Executable         string                `json:"executable"`
	ResolvedPath       string                `json:"resolved_path"`
	CustomPath         string                `json:"custom_path"`
	Source             agent.Source          `json:"source"`
	Version            string                `json:"version"`
	State              agent.ConnectionState `json:"state"`
	Supported          bool                  `json:"supported"`
	UnsupportedReason  string                `json:"unsupported_reason"`
	AuthState          agent.AuthState       `json:"auth_state"`
	AccountInfo        string                `json:"account_info"`
	IsolatedProfileDir string                `json:"isolated_profile_dir"`
	ProtocolVersion    int                   `json:"protocol_version"`
	AgentName          string                `json:"agent_name"`
	AgentVersion       string                `json:"agent_version"`
	LastError          string                `json:"last_error"`
	LastErrorKey       string                `json:"last_error_key,omitempty"`
	LastErrorParams    map[string]string     `json:"last_error_params,omitempty"`
	InstallHint        string                `json:"install_hint"`
	Active             bool                  `json:"active"`
	Busy               bool                  `json:"busy"`
	Models             []agent.ModelOption   `json:"models"`
	CurrentModel       string                `json:"current_model"`
	SelectedModel      string                `json:"selected_model"`
	SelectedEffort     string                `json:"selected_effort"`
}

// AgentSetModelPayload selects the model (and optional reasoning effort) used by a provider.
type AgentSetModelPayload struct {
	ID     agent.ProviderID `json:"id"`
	Model  string           `json:"model"`
	Effort string           `json:"effort"`
}

// Validate checks the provider identifier.
func (p AgentSetModelPayload) Validate() error {
	return agent.ValidateProviderID(p.ID)
}

// NewAgentProviderInfo converts a domain agent.ProviderInfo to the wire DTO.
func NewAgentProviderInfo(in agent.ProviderInfo) AgentProviderInfo {
	return AgentProviderInfo(in)
}

// AgentListResult answers agent:list and agent:disconnect.
type AgentListResult struct {
	Providers []AgentProviderInfo `json:"providers"`
}

// AgentProviderIDPayload targets a specific agent provider.
type AgentProviderIDPayload struct {
	ID agent.ProviderID `json:"id"`
}

// Validate checks the provider identifier.
func (p AgentProviderIDPayload) Validate() error {
	return agent.ValidateProviderID(p.ID)
}

// AgentLoginPayload authenticates an isolated provider profile (with optional API key).
type AgentLoginPayload struct {
	ID     agent.ProviderID `json:"id"`
	APIKey string           `json:"api_key"`
}

// Validate checks the provider identifier.
func (p AgentLoginPayload) Validate() error {
	return agent.ValidateProviderID(p.ID)
}

// AgentSetPathPayload sets or clears a custom executable path for a provider.
type AgentSetPathPayload struct {
	ID         agent.ProviderID `json:"id"`
	CustomPath string           `json:"custom_path"`
}

// Validate checks the provider identifier.
func (p AgentSetPathPayload) Validate() error {
	return agent.ValidateProviderID(p.ID)
}

// AgentSendPromptPayload submits a user chat message to the active agent.
type AgentSendPromptPayload struct {
	Prompt string `json:"prompt"`
}

// Validate rejects blank prompts.
func (p AgentSendPromptPayload) Validate() error {
	if strings.TrimSpace(p.Prompt) == "" {
		return apperr.Validation("prompt is required")
	}
	return nil
}

// AgentPromptResult acknowledges acceptance of an asynchronous prompt turn.
type AgentPromptResult struct {
	MessageID  string           `json:"message_id"`
	ProviderID agent.ProviderID `json:"provider_id"`
}

// AgentChatEvent streams delta/thought/tool/completion/error updates from the active turn.
type AgentChatEvent struct {
	Type        agent.ChatEventType `json:"type"`
	ProviderID  agent.ProviderID    `json:"provider_id"`
	MessageID   string              `json:"message_id"`
	Text        string              `json:"text"`
	Tool        string              `json:"tool,omitempty"`
	Error       string              `json:"error"`
	ErrorKey    string              `json:"error_key,omitempty"`
	ErrorParams map[string]string   `json:"error_params,omitempty"`
}

// NewAgentChatEvent converts a domain agent.ChatEvent to the wire DTO.
func NewAgentChatEvent(in agent.ChatEvent) AgentChatEvent {
	return AgentChatEvent(in)
}

// AgentChatMessage represents one persisted chat entry in DezuxkStudio history.
type AgentChatMessage struct {
	ID         string           `json:"id"`
	ProviderID agent.ProviderID `json:"provider_id"`
	Role       string           `json:"role"`
	Text       string           `json:"text"`
	Thought    string           `json:"thought,omitempty"`
	Tool       string           `json:"tool,omitempty"`
	CreatedAt  int64            `json:"created_at"`
}

// AgentHistoryResult returns persisted DezuxkStudio chat history.
type AgentHistoryResult struct {
	Messages []AgentChatMessage `json:"messages"`
}

// AgentApprovalRequest describes a high-impact action waiting for desktop UI approval.
type AgentApprovalRequest struct {
	ID         string           `json:"id"`
	ProviderID agent.ProviderID `json:"provider_id"`
	Tool       string           `json:"tool"`
	Title      string           `json:"title"`
	Summary    string           `json:"summary"`
	CreatedAt  string           `json:"created_at"`
}

// AgentApprovalListResult returns pending agent approvals.
type AgentApprovalListResult struct {
	Approvals []AgentApprovalRequest `json:"approvals"`
}

// NewAgentApprovalListResult converts domain approval requests into the wire DTO.
func NewAgentApprovalListResult(in []agent.ApprovalRequest) AgentApprovalListResult {
	out := make([]AgentApprovalRequest, len(in))
	for i, item := range in {
		out[i] = AgentApprovalRequest(item)
	}
	return AgentApprovalListResult{Approvals: out}
}

// AgentResolveApprovalPayload approves or rejects a pending approval card.
type AgentResolveApprovalPayload struct {
	ID      string `json:"id"`
	Approve bool   `json:"approve"`
}

// Validate checks approval ID presence.
func (p AgentResolveApprovalPayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return apperr.Validation("approval id is required")
	}
	return nil
}

// AgentUIContextPayload updates the active screen context visible to the agent.
type AgentUIContextPayload struct {
	Route         string            `json:"route"`
	Note          string            `json:"note"`
	Screen        string            `json:"screen,omitempty"`
	Feature       string            `json:"feature,omitempty"`
	SelectedFiles []string          `json:"selected_files,omitempty"`
	Settings      map[string]string `json:"settings,omitempty"`
	LastResult    string            `json:"last_result,omitempty"`
}

// AgentAllowedDirsResult returns the authorized workspace folder list.
type AgentAllowedDirsResult struct {
	Dirs []string `json:"dirs"`
}

// AgentAllowedDirPayload identifies a workspace folder to grant or revoke.
type AgentAllowedDirPayload struct {
	Dir string `json:"dir"`
}

// Validate checks dir presence.
func (p AgentAllowedDirPayload) Validate() error {
	if strings.TrimSpace(p.Dir) == "" {
		return apperr.Validation("directory path is required")
	}
	return nil
}
