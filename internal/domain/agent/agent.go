// Package agent defines the domain models, provider registry, and interfaces for isolated AI agents.
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"app/internal/apperr"
)

// ProviderID identifies a supported external agent provider.
type ProviderID string

const (
	ProviderAntigravity ProviderID = "antigravity"
	ProviderCodex       ProviderID = "codex"
	ProviderClaude      ProviderID = "claude"
)

// AllProviderIDs returns all registered provider identifiers in display order.
func AllProviderIDs() []ProviderID {
	ids := make([]ProviderID, len(providerSpecs))
	for i, sp := range providerSpecs {
		ids[i] = sp.ID
	}
	return ids
}

// ConnectionState describes the runtime lifecycle state of a provider connection.
type ConnectionState string

const (
	StateMissing         ConnectionState = "missing"
	StateDisconnected    ConnectionState = "disconnected"
	StateConnecting      ConnectionState = "connecting"
	StateConnected       ConnectionState = "connected"
	StateUnauthenticated ConnectionState = "unauthenticated"
	StateError           ConnectionState = "error"
)

// AllConnectionStates returns every valid ConnectionState.
func AllConnectionStates() []ConnectionState {
	return []ConnectionState{
		StateMissing,
		StateDisconnected,
		StateConnecting,
		StateConnected,
		StateUnauthenticated,
		StateError,
	}
}

// AuthState describes the authentication state of a provider's isolated profile.
type AuthState string

const (
	AuthUnknown         AuthState = "unknown"
	AuthAuthenticated   AuthState = "authenticated"
	AuthUnauthenticated AuthState = "unauthenticated"
	AuthUnsupported     AuthState = "unsupported"
)

// AllAuthStates returns every valid AuthState.
func AllAuthStates() []AuthState {
	return []AuthState{
		AuthUnknown,
		AuthAuthenticated,
		AuthUnauthenticated,
		AuthUnsupported,
	}
}

// Source indicates where the provider executable was resolved.
type Source string

const (
	SourceCustom Source = "custom"
	SourceSystem Source = "system"
	// SourceManaged is the private copy the app downloaded and keeps up to date itself.
	SourceManaged Source = "managed"
	SourceNone    Source = "none"
)

// AllSources returns every valid Source.
func AllSources() []Source {
	return []Source{SourceCustom, SourceManaged, SourceSystem, SourceNone}
}

// ChatEventType classifies streamed events sent from an active agent turn to the UI.
type ChatEventType string

const (
	ChatEventDelta      ChatEventType = "delta"
	ChatEventThought    ChatEventType = "thought"
	ChatEventToolCall   ChatEventType = "tool_call"
	ChatEventToolResult ChatEventType = "tool_result"
	ChatEventDone       ChatEventType = "done"
	ChatEventError      ChatEventType = "error"
	ChatEventSystem     ChatEventType = "system"
)

// AllChatEventTypes returns every valid ChatEventType.
func AllChatEventTypes() []ChatEventType {
	return []ChatEventType{
		ChatEventDelta,
		ChatEventThought,
		ChatEventToolCall,
		ChatEventToolResult,
		ChatEventDone,
		ChatEventError,
		ChatEventSystem,
	}
}

// ProviderSpec declares static metadata and isolated launch configuration for a provider.
type ProviderSpec struct {
	ID                   ProviderID
	Name                 string
	ExecutableLabel      string
	CandidateExecutables []string
	KnownPaths           []string
	ACPArgs              []string
	VersionArgs          []string
	// CredentialEnv lists provider credential variables stripped from the inherited environment so the
	// isolated profile never silently uses the user's personal keys.
	CredentialEnv []string
	// AuthMethodPreference lists ACP auth method IDs to try for interactive sign-in, best first; the
	// first one the agent advertises in initialize wins.
	AuthMethodPreference []string
	// ModelAliases are model choices the CLI accepts but cannot enumerate itself.
	ModelAliases []ModelOption
	// Install says where the CLI is published so the app can keep a private, self-updating copy.
	Install InstallSpec
	Login   LoginSpec
	// SessionGoneErrors are (lower-case) phrases the CLI uses when a saved session can no longer be
	// resumed; such a turn is retried in a new session primed with the chat history.
	SessionGoneErrors []string
	Supported         bool
	UnsupportedReason string
}

// providerSpecs declares every provider once, in display order.
var providerSpecs = []ProviderSpec{
	{
		ID:                   ProviderAntigravity,
		Name:                 "Google Antigravity",
		ExecutableLabel:      "agy",
		CandidateExecutables: []string{"agy", "agy.exe", "agy.cmd"},
		KnownPaths:           []string{"${LOCALAPPDATA}/agy/bin/agy.exe"},
		ACPArgs:              []string{"acp", "--include-default-customizations=false", "--sandbox"},
		VersionArgs:          []string{"--version"},
		CredentialEnv:        []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		AuthMethodPreference: []string{"oauth-personal"},
		Install: InstallSpec{
			Scheme:     SchemeManifest,
			BaseURL:    "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app",
			Platforms:  map[string]string{"windows_amd64": "windows_amd64", "windows_arm64": "windows_arm64"},
			Executable: "agy.exe",
			Env:        map[string]string{"AGY_CLI_DISABLE_AUTO_UPDATE": "1"},
		},
		Supported: true,
	},
	{
		ID:                   ProviderCodex,
		Name:                 "OpenAI Codex",
		ExecutableLabel:      "codex",
		CandidateExecutables: []string{"codex", "codex.exe", "codex.cmd"},
		KnownPaths: []string{
			"${LOCALAPPDATA}/Programs/OpenAI/Codex/bin/codex.exe",
			"${APPDATA}/npm/codex.cmd",
			"${USERPROFILE}/.cargo/bin/codex.exe",
		},
		ACPArgs:       nil,
		VersionArgs:   []string{"--version"},
		CredentialEnv: []string{"OPENAI_API_KEY"},
		Login:         LoginSpec{Args: []string{"login", "--device-auth"}},
		// `codex exec resume` and the app-server's thread/resume.
		SessionGoneErrors: []string{"no rollout found", "thread/resume failed"},
		Install: InstallSpec{
			Scheme:        SchemeReleasePackage,
			BaseURL:       "https://github.com/openai/codex/releases/download",
			LatestAPI:     "https://api.github.com/repos/openai/codex/releases/latest",
			TagPrefix:     "rust-v",
			Platforms:     map[string]string{"windows_amd64": "x86_64-pc-windows-msvc", "windows_arm64": "aarch64-pc-windows-msvc"},
			Executable:    "bin/codex.exe",
			PackageAsset:  "codex-package-{platform}.tar.gz",
			ChecksumAsset: "codex-package_SHA256SUMS",
		},
		Supported: true,
	},
	{
		ID:                   ProviderClaude,
		Name:                 "Anthropic Claude",
		ExecutableLabel:      "claude",
		CandidateExecutables: []string{"claude", "claude.exe", "claude.cmd"},
		KnownPaths: []string{
			"${USERPROFILE}/.local/bin/claude.exe",
			"${APPDATA}/npm/claude.cmd",
		},
		ACPArgs:       nil,
		VersionArgs:   []string{"--version"},
		CredentialEnv: []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"},
		Login:         LoginSpec{Args: []string{"auth", "login"}, PasteCode: true, OpensBrowser: true},
		// `claude --resume`.
		SessionGoneErrors: []string{"no conversation found with session id"},
		Install: InstallSpec{
			Scheme:     SchemeVersionFile,
			BaseURL:    "https://downloads.claude.ai/claude-code-releases",
			LatestPath: "latest",
			Platforms:  map[string]string{"windows_amd64": "win32-x64", "windows_arm64": "win32-arm64"},
			Executable: "claude.exe",
			Env:        map[string]string{"DISABLE_AUTOUPDATER": "1"},
		},
		// Claude Code's built-in aliases; the CLI maps each one to the latest model of that family.
		ModelAliases: []ModelOption{
			{ID: "fable", Name: "Claude Fable (latest)", Description: "Most capable model family"},
			{ID: "opus", Name: "Claude Opus (latest)", Description: "Deep reasoning for complex tasks"},
			{ID: "sonnet", Name: "Claude Sonnet (latest)", Description: "Balanced speed and intelligence"},
			{ID: "haiku", Name: "Claude Haiku (latest)", Description: "Fastest responses"},
		},
		Supported: true,
	},
}

// Registry maps each ProviderID to its static specification.
var Registry = func() map[ProviderID]ProviderSpec {
	m := make(map[ProviderID]ProviderSpec, len(providerSpecs))
	for _, sp := range providerSpecs {
		m[sp.ID] = sp
	}
	return m
}()

// ModelOption is one model a provider's CLI reports as selectable, with the reasoning levels it supports.
// Lists always come from the provider itself (ACP session, CLI catalog, or CLI aliases), never guessed.
type ModelOption struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

// ModelSelection is the user's persisted model choice for a provider. Empty Model means the provider's default.
type ModelSelection struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// ProviderInfo describes the live inspection, isolated auth, and connection status of an agent provider.
type ProviderInfo struct {
	ID                 ProviderID        `json:"id"`
	Name               string            `json:"name"`
	Executable         string            `json:"executable"`
	ResolvedPath       string            `json:"resolved_path"`
	CustomPath         string            `json:"custom_path"`
	Source             Source            `json:"source"`
	Version            string            `json:"version"`
	State              ConnectionState   `json:"state"`
	Supported          bool              `json:"supported"`
	UnsupportedReason  string            `json:"unsupported_reason"`
	AuthState          AuthState         `json:"auth_state"`
	AccountInfo        string            `json:"account_info"`
	IsolatedProfileDir string            `json:"isolated_profile_dir"`
	ProtocolVersion    int               `json:"protocol_version"`
	AgentName          string            `json:"agent_name"`
	AgentVersion       string            `json:"agent_version"`
	LastError          string            `json:"last_error"`
	LastErrorKey       string            `json:"last_error_key,omitempty"`    // frontend i18n key for LastError
	LastErrorParams    map[string]string `json:"last_error_params,omitempty"` // values for the key's placeholders
	Active             bool              `json:"active"`
	Busy               bool              `json:"busy"`
	Models             []ModelOption     `json:"models"`
	CurrentModel       string            `json:"current_model"`
	SelectedModel      string            `json:"selected_model"`
	SelectedEffort     string            `json:"selected_effort"`
	// Revision grows with every status snapshot the service builds. A snapshot that reaches the UI
	// late (a slow agent:list) must never replace a newer one (e.g. "connected, with models").
	Revision int64 `json:"revision"`
	// Installing is true while the app downloads or updates its own copy of the CLI; InstallPercent is 0-100.
	Installing     bool    `json:"installing"`
	InstallPercent float64 `json:"install_percent"`
}

// ChatEvent is broadcast when an active agent streams text, thoughts, tool calls, or completion updates.
type ChatEvent struct {
	Type       ChatEventType `json:"type"`
	ProviderID ProviderID    `json:"provider_id"`
	MessageID  string        `json:"message_id"`
	Text       string        `json:"text"`
	Tool       string        `json:"tool,omitempty"`
	Error      string        `json:"error"`
	// ErrorKey/ErrorParams localize Error in the UI (frontend i18n key and placeholder values).
	ErrorKey    string            `json:"error_key,omitempty"`
	ErrorParams map[string]string `json:"error_params,omitempty"`
}

// ChatMessage stores one persisted message in DezuxkStudio's own SQLite history.
type ChatMessage struct {
	ID         string     `json:"id"`
	ProviderID ProviderID `json:"provider_id"`
	Role       string     `json:"role"`
	Text       string     `json:"text"`
	Thought    string     `json:"thought,omitempty"`
	Tool       string     `json:"tool,omitempty"`
	CreatedAt  int64      `json:"created_at"`
	// ConversationID groups messages into one chat; empty only for rows written before conversations.
	ConversationID string `json:"conversation_id,omitempty"`
}

// Conversation is one chat thread in DezuxkStudio's history.
type Conversation struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"` // first user message, shortened
	ProviderID   ProviderID `json:"provider_id"`
	CreatedAt    int64      `json:"created_at"`
	UpdatedAt    int64      `json:"updated_at"`
	MessageCount int        `json:"message_count"`
}

// ApprovalRequest represents a pending high-impact action proposed by the agent for desktop UI approval.
type ApprovalRequest struct {
	ID         string     `json:"id"`
	ProviderID ProviderID `json:"provider_id"`
	Tool       string     `json:"tool"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	CreatedAt  string     `json:"created_at"`
}

// ValidateProviderID checks that id belongs to the registered provider set.
func ValidateProviderID(id ProviderID) error {
	if !slices.Contains(AllProviderIDs(), id) {
		return apperr.Validation(fmt.Sprintf("unsupported agent provider %q", id))
	}
	return nil
}

// Repository persists agent provider settings, isolated workspace permissions, and chat history.
type Repository interface {
	GetCustomPath(ctx context.Context, id ProviderID) (string, error)
	GetAllCustomPaths(ctx context.Context) (map[ProviderID]string, error)
	SetCustomPath(ctx context.Context, id ProviderID, customPath string) error
	AppendChatMessage(ctx context.Context, msg ChatMessage) error
	ListChatMessages(ctx context.Context, limit int) ([]ChatMessage, error)
	ClearChatMessages(ctx context.Context) error
	// RedactChatMessages replaces every occurrence of secret in stored chat text.
	RedactChatMessages(ctx context.Context, secret, replacement string) error
	// SaveConversation creates the conversation or updates its provider and updated_at; a non-empty
	// title is only set when the conversation has none yet.
	SaveConversation(ctx context.Context, c Conversation) error
	ListConversations(ctx context.Context, limit int) ([]Conversation, error)
	ListConversationMessages(ctx context.Context, conversationID string, limit int) ([]ChatMessage, error)
	GetConversationSessions(ctx context.Context, conversationID string) (map[ProviderID]string, error)
	SetConversationSession(ctx context.Context, conversationID string, providerID ProviderID, sessionID string) error
	DeleteConversation(ctx context.Context, conversationID string) error
	ListAllowedDirs(ctx context.Context) ([]string, error)
	AddAllowedDir(ctx context.Context, dir string) error
	RemoveAllowedDir(ctx context.Context, dir string) error
	GetModelSelection(ctx context.Context, id ProviderID) (ModelSelection, error)
	SetModelSelection(ctx context.Context, id ProviderID, sel ModelSelection) error
	// RememberedConnection is the provider the user left connected ("" = none); it is reconnected at start-up.
	RememberedConnection(ctx context.Context) (ProviderID, error)
	SetRememberedConnection(ctx context.Context, id ProviderID) error
}

// IsSessionGone reports whether err is the CLI saying the saved session cannot be resumed.
func (p ProviderSpec) IsSessionGone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, phrase := range p.SessionGoneErrors {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}
