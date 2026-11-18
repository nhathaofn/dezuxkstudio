package contracts

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
	FolderLogs                FolderTarget = "logs"
	FolderData                FolderTarget = "data"
	FolderConfig              FolderTarget = "config"
	FolderDiagnostics         FolderTarget = "diagnostics"
	FolderAgentCustomizations FolderTarget = "agent_customizations"
	FolderWorkspaceRoot       FolderTarget = "workspace_root"
)

// FolderTargets lists valid targets.
func FolderTargets() []FolderTarget {
	return []FolderTarget{FolderLogs, FolderData, FolderConfig, FolderDiagnostics, FolderAgentCustomizations, FolderWorkspaceRoot}
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
	Theme                    preferences.Theme    `json:"theme"`
	Language                 preferences.Language `json:"language"`
	WorkspaceRoot            string               `json:"workspace_root"`
	OnboardingCompleted      bool                 `json:"onboarding_completed"`
	AgentSkipTrustedApproval bool                 `json:"agent_skip_trusted_approval"`
}

// NewPreferences converts the domain model.
func NewPreferences(p preferences.Preferences) Preferences {
	return Preferences{
		Theme:                    p.Theme,
		Language:                 p.Language,
		WorkspaceRoot:            p.WorkspaceRoot,
		OnboardingCompleted:      p.OnboardingCompleted,
		AgentSkipTrustedApproval: p.AgentSkipTrustedApproval,
	}
}

// PreferencesPatch is the payload of preferences:update; omitted fields are unchanged.
type PreferencesPatch struct {
	Theme                    *preferences.Theme    `json:"theme,omitempty"`
	Language                 *preferences.Language `json:"language,omitempty"`
	WorkspaceRoot            *string               `json:"workspace_root,omitempty"`
	OnboardingCompleted      *bool                 `json:"onboarding_completed,omitempty"`
	AgentSkipTrustedApproval *bool                 `json:"agent_skip_trusted_approval,omitempty"`
}

// Validate rejects empty patches or invalid values.
func (p PreferencesPatch) Validate() error {
	if p.Theme == nil && p.Language == nil && p.WorkspaceRoot == nil && p.OnboardingCompleted == nil && p.AgentSkipTrustedApproval == nil {
		return apperr.Validation("patch must change at least one field")
	}
	if p.WorkspaceRoot != nil {
		if strings.ContainsRune(*p.WorkspaceRoot, 0) {
			return apperr.Validation("workspace_root must not contain NUL bytes").
				WithDetails(map[string]any{"field": "workspace_root"})
		}
		if trimmed := strings.TrimSpace(*p.WorkspaceRoot); trimmed != "" {
			if !filepath.IsAbs(trimmed) {
				return apperr.Validation(fmt.Sprintf("workspace_root must be an absolute path, got %q", *p.WorkspaceRoot)).
					WithDetails(map[string]any{"field": "workspace_root"})
			}
		} else if *p.WorkspaceRoot != "" {
			return apperr.Validation(fmt.Sprintf("workspace_root must be an absolute path, got %q", *p.WorkspaceRoot)).
				WithDetails(map[string]any{"field": "workspace_root"})
		}
	}
	return nil
}

// ToDomain converts to the domain patch.
func (p PreferencesPatch) ToDomain() preferences.Patch {
	return preferences.Patch{
		Theme:                    p.Theme,
		Language:                 p.Language,
		WorkspaceRoot:            p.WorkspaceRoot,
		OnboardingCompleted:      p.OnboardingCompleted,
		AgentSkipTrustedApproval: p.AgentSkipTrustedApproval,
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
	// MsgKey/MsgParams name a UI translation of Message; clients without the key show Message.
	MsgKey    string            `json:"msg_key,omitempty"`
	MsgParams map[string]string `json:"msg_params,omitempty"`
	// Heartbeat marks a keep-alive: it re-arms the request timeout and is not shown in the UI.
	Heartbeat bool `json:"heartbeat,omitempty"`
	// ResumedFromBytes indicates the byte offset from which a download was resumed (0 if fresh).
	ResumedFromBytes int64 `json:"resumed_from_bytes,omitempty"`
	// IsResume reports whether the current operation resumed an earlier partial transfer.
	IsResume bool `json:"is_resume,omitempty"`
	// BinaryProgress provides structured progress metrics when a binary download is running.
	BinaryProgress *BinaryProgress `json:"binary_progress,omitempty"`
}

// BinaryProgress reports download progress details for managed external binaries.
type BinaryProgress struct {
	ID               binaries.BinaryID `json:"id"`
	TotalBytes       int64             `json:"total_bytes"`
	DownloadedBytes  int64             `json:"downloaded_bytes"`
	ResumedFromBytes int64             `json:"resumed_from_bytes"`
	IsResume         bool              `json:"is_resume"`
	Percent          float64           `json:"percent"`
	SpeedBPS         int64             `json:"speed_bps,omitempty"`
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
	ID                  agent.ProviderID      `json:"id"`
	Name                string                `json:"name"`
	Executable          string                `json:"executable"`
	ResolvedPath        string                `json:"resolved_path"`
	CustomPath          string                `json:"custom_path"`
	Source              agent.Source          `json:"source"`
	Version             string                `json:"version"`
	State               agent.ConnectionState `json:"state"`
	Supported           bool                  `json:"supported"`
	UnsupportedReason   string                `json:"unsupported_reason"`
	AuthState           agent.AuthState       `json:"auth_state"`
	AccountInfo         string                `json:"account_info"`
	IsolatedProfileDir  string                `json:"isolated_profile_dir"`
	ProtocolVersion     int                   `json:"protocol_version"`
	AgentName           string                `json:"agent_name"`
	AgentVersion        string                `json:"agent_version"`
	LastError           string                `json:"last_error"`
	LastErrorKey        string                `json:"last_error_key,omitempty"`
	LastErrorParams     map[string]string     `json:"last_error_params,omitempty"`
	Active              bool                  `json:"active"`
	Busy                bool                  `json:"busy"`
	Models              []agent.ModelOption   `json:"models"`
	CurrentModel        string                `json:"current_model"`
	SelectedModel       string                `json:"selected_model"`
	SelectedEffort      string                `json:"selected_effort"`
	Revision            int64                 `json:"revision"`
	Installing          bool                  `json:"installing"`
	InstallPercent      float64               `json:"install_percent"`
	CompatibleProviders []string              `json:"compatible_providers,omitempty"`
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
	return AgentProviderInfo{
		ID:                 in.ID,
		Name:               in.Name,
		Executable:         in.Executable,
		ResolvedPath:       in.ResolvedPath,
		CustomPath:         in.CustomPath,
		Source:             in.Source,
		Version:            in.Version,
		State:              in.State,
		Supported:          in.Supported,
		UnsupportedReason:  in.UnsupportedReason,
		AuthState:          in.AuthState,
		AccountInfo:        in.AccountInfo,
		IsolatedProfileDir: in.IsolatedProfileDir,
		ProtocolVersion:    in.ProtocolVersion,
		AgentName:          in.AgentName,
		AgentVersion:       in.AgentVersion,
		LastError:          in.LastError,
		LastErrorKey:       in.LastErrorKey,
		LastErrorParams:    in.LastErrorParams,
		Active:             in.Active,
		Busy:               in.Busy,
		Models:             in.Models,
		CurrentModel:       in.CurrentModel,
		SelectedModel:      in.SelectedModel,
		SelectedEffort:     in.SelectedEffort,
		Revision:           in.Revision,
		Installing:         in.Installing,
		InstallPercent:     in.InstallPercent,
	}
}

// AgentListResult answers agent:list and agent:disconnect.
type AgentListResult struct {
	Providers     []AgentProviderInfo `json:"providers"`
	DeveloperMode bool                `json:"developer_mode,omitempty"`
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

// AgentLoginCodePayload carries the code the user copied from a sign-in page.
type AgentLoginCodePayload struct {
	ID   agent.ProviderID `json:"id"`
	Code string           `json:"code"`
}

// Validate checks the provider identifier.
func (p AgentLoginCodePayload) Validate() error {
	return agent.ValidateProviderID(p.ID)
}

// AgentLoginPrompt tells the UI what the user must do to finish a running sign-in.
type AgentLoginPrompt struct {
	ProviderID agent.ProviderID `json:"provider_id"`
	URL        string           `json:"url"`
	Code       string           `json:"code"`
	PasteCode  bool             `json:"paste_code"`
	OpenURL    bool             `json:"open_url"`
}

// NewAgentLoginPrompt converts the domain prompt into the wire DTO.
func NewAgentLoginPrompt(in agent.LoginPrompt) AgentLoginPrompt { return AgentLoginPrompt(in) }

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
	// ConversationID is the chat the prompt was added to (created by the first message of a new chat).
	ConversationID string `json:"conversation_id"`
}

// AgentChatEvent streams delta/thought/tool/completion/error updates from the active turn.
type AgentChatEvent struct {
	Type             agent.ChatEventType `json:"type"`
	ProviderID       agent.ProviderID    `json:"provider_id"`
	MessageID        string              `json:"message_id"`
	Text             string              `json:"text"`
	Tool             string              `json:"tool,omitempty"`
	SubagentID       string              `json:"subagent_id,omitempty"`
	SubagentName     string              `json:"subagent_name,omitempty"`
	SubagentProvider agent.ProviderID    `json:"subagent_provider,omitempty"`
	Error            string              `json:"error"`
	ErrorKey         string              `json:"error_key,omitempty"`
	ErrorParams      map[string]string   `json:"error_params,omitempty"`
}

// NewAgentChatEvent converts a domain agent.ChatEvent to the wire DTO.
func NewAgentChatEvent(in agent.ChatEvent) AgentChatEvent {
	return AgentChatEvent(in)
}

// AgentChatMessage represents one persisted chat entry in DezuxkStudio history.
type AgentChatMessage struct {
	ID         string                     `json:"id"`
	ProviderID agent.ProviderID           `json:"provider_id"`
	Role       string                     `json:"role"`
	Text       string                     `json:"text"`
	Thought    string                     `json:"thought,omitempty"`
	Tool       string                     `json:"tool,omitempty"`
	Subagents  []agent.SubagentInvocation `json:"subagents,omitempty"`
	CreatedAt  int64                      `json:"created_at"`
	// ConversationID is the chat the message belongs to.
	ConversationID string `json:"conversation_id,omitempty"`
}

// AgentHistoryResult returns one chat's messages ("" conversation_id = a new, empty chat).
type AgentHistoryResult struct {
	ConversationID string             `json:"conversation_id"`
	Messages       []AgentChatMessage `json:"messages"`
}

// AgentConversation summarizes one agent chat.
type AgentConversation struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	ProviderID   agent.ProviderID `json:"provider_id"`
	CreatedAt    int64            `json:"created_at"`
	UpdatedAt    int64            `json:"updated_at"`
	MessageCount int              `json:"message_count"`
}

// AgentConversationListResult answers agent:list_conversations.
type AgentConversationListResult struct {
	Conversations []AgentConversation `json:"conversations"`
}

// AgentConversationIDPayload targets one agent chat.
type AgentConversationIDPayload struct {
	ID string `json:"id"`
}

// Validate checks the conversation ID.
func (p AgentConversationIDPayload) Validate() error {
	if id := strings.TrimSpace(p.ID); id == "" || len(id) > 64 {
		return apperr.Validation("conversation id is required")
	}
	return nil
}

// Approval status values for agent MCP tool invocations.
const (
	ApprovalStatusPending   = "pending"
	ApprovalStatusExecuting = "executing"
	ApprovalStatusApproved  = "approved"
	ApprovalStatusRejected  = "rejected"
	ApprovalStatusFailed    = "failed"
	ApprovalStatusAmbiguous = "ambiguous"
)

// AgentApproval is an alias for AgentApprovalRequest.
type AgentApproval = AgentApprovalRequest

// AgentApprovalRequest describes a high-impact action waiting for desktop UI approval.
type AgentApprovalRequest struct {
	ID                string           `json:"id"`
	ProviderID        agent.ProviderID `json:"provider_id"`
	Tool              string           `json:"tool"`
	Title             string           `json:"title"`
	Summary           string           `json:"summary"`
	CreatedAt         string           `json:"created_at"`
	InputHash         string           `json:"input_hash,omitempty"`
	Status            string           `json:"status,omitempty"`
	ContinuationDepth int              `json:"continuation_depth,omitempty"`
}

// AgentApprovalListResult returns pending agent approvals.
type AgentApprovalListResult struct {
	Approvals []AgentApprovalRequest `json:"approvals"`
}

// NewAgentApprovalListResult converts domain approval requests into the wire DTO.
func NewAgentApprovalListResult(in []agent.ApprovalRequest) AgentApprovalListResult {
	out := make([]AgentApprovalRequest, len(in))
	for i, item := range in {
		out[i] = AgentApprovalRequest{
			ID:         item.ID,
			ProviderID: item.ProviderID,
			Tool:       item.Tool,
			Title:      item.Title,
			Summary:    item.Summary,
			CreatedAt:  item.CreatedAt,
			Status:     ApprovalStatusPending,
		}
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

// AgentCustomizationsResult returns all shared skills, subagents, and delegatable tool names.
type AgentCustomizationsResult struct {
	Dir            string               `json:"dir"`
	Skills         []agent.SkillInfo    `json:"skills"`
	Subagents      []agent.SubagentInfo `json:"subagents"`
	AvailableTools []string             `json:"available_tools"`
}

// NewAgentCustomizationsResult converts domainagent.CustomizationsSnapshot to the wire DTO.
func NewAgentCustomizationsResult(in agent.CustomizationsSnapshot) AgentCustomizationsResult {
	return AgentCustomizationsResult(in)
}

// AgentCustomizationIDPayload targets one skill or subagent by slug name.
type AgentCustomizationIDPayload struct {
	Name string `json:"name"`
}

// Validate checks customization name presence.
func (p AgentCustomizationIDPayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return apperr.Validation("customization name is required")
	}
	return nil
}

// AgentSaveSkillPayload creates or updates a shared skill markdown file.
type AgentSaveSkillPayload struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	UserInvocable bool   `json:"user_invocable"`
	Enabled       bool   `json:"enabled"`
	Body          string `json:"body"`
}

// Validate checks required skill fields.
func (p AgentSaveSkillPayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return apperr.Validation("skill name is required")
	}
	if strings.TrimSpace(p.Description) == "" {
		return apperr.Validation("skill description is required")
	}
	return nil
}

// AgentSaveSubagentPayload creates or updates a shared subagent markdown file.
type AgentSaveSubagentPayload struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Tools       []string         `json:"tools"`
	Skills      []string         `json:"skills"`
	Provider    agent.ProviderID `json:"provider,omitempty"`
	Model       string           `json:"model,omitempty"`
	Enabled     bool             `json:"enabled"`
	Body        string           `json:"body"`
}

// Validate checks required subagent fields and provider override validity.
func (p AgentSaveSubagentPayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return apperr.Validation("subagent name is required")
	}
	if strings.TrimSpace(p.Description) == "" {
		return apperr.Validation("subagent description is required")
	}
	if p.Provider != "" {
		if err := agent.ValidateProviderID(p.Provider); err != nil {
			return err
		}
	}
	return nil
}

// MediaAssetItem represents a registered media asset across features.
type MediaAssetItem struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	SourceNode     string  `json:"source_node"`
	FilePath       string  `json:"file_path"`
	RegisteredRoot string  `json:"registered_root"`
	MIMEType       string  `json:"mime_type"`
	SizeBytes      int64   `json:"size_bytes"`
	Width          int     `json:"width,omitempty"`
	Height         int     `json:"height,omitempty"`
	DurationSec    float64 `json:"duration_sec,omitempty"`
	MetadataJSON   string  `json:"metadata_json,omitempty"`
	CreatedAt      int64   `json:"created_at"`
	LastVerifiedAt int64   `json:"last_verified_at"`
}

// MediaListPayload is the filter payload for media:list.
type MediaListPayload struct {
	Kind   string `json:"kind,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Search string `json:"search,omitempty"`
}

// MediaListResult answers media:list.
type MediaListResult struct {
	Assets []MediaAssetItem `json:"assets"`
	Total  int              `json:"total"`
}

// MediaDeletePayload targets an asset for deletion in media:delete.
type MediaDeletePayload struct {
	ID         string `json:"id"`
	DeleteFile bool   `json:"delete_file,omitempty"`
}

// Validate checks the asset ID.
func (p MediaDeletePayload) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return apperr.Validation("id is required")
	}
	return nil
}

// MediaSignURLPayload requests a signed scoped streaming/download URL in media:sign_url.
type MediaSignURLPayload struct {
	AssetID    string `json:"asset_id"`
	Purpose    string `json:"purpose,omitempty"`     // "preview" | "download"
	TTLSeconds int    `json:"ttl_seconds,omitempty"` // optional custom TTL in seconds
}

// Validate checks the asset ID, optional purpose, and non-negative TTL.
func (p MediaSignURLPayload) Validate() error {
	if strings.TrimSpace(p.AssetID) == "" {
		return apperr.Validation("asset_id is required")
	}
	if p.Purpose != "" && p.Purpose != "preview" && p.Purpose != "download" {
		return apperr.Validation(fmt.Sprintf("invalid purpose %q: must be preview or download", p.Purpose))
	}
	if p.TTLSeconds < 0 {
		return apperr.Validation("ttl_seconds must be >= 0")
	}
	return nil
}

// MediaSignedURLResult returns a generated signed URL and ticket metadata.
type MediaSignedURLResult struct {
	AssetID   string `json:"asset_id"`
	URL       string `json:"url"`
	Ticket    string `json:"ticket"`
	ExpiresAt int64  `json:"expires_at"`
	Purpose   string `json:"purpose"`
}

// MediaRegisterInputPayload registers a local media file as an asset in media:register_input.
type MediaRegisterInputPayload struct {
	FilePath   string `json:"file_path"`
	Kind       string `json:"kind,omitempty"`
	SourceNode string `json:"source_node,omitempty"`
}

// Validate checks the file path.
func (p MediaRegisterInputPayload) Validate() error {
	if strings.TrimSpace(p.FilePath) == "" {
		return apperr.Validation("file_path is required")
	}
	return nil
}
