package contracts

import (
	"encoding/json"
	"reflect"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/domain/agent"
	"app/internal/domain/binaries"
	"app/internal/domain/preferences"
	"app/internal/domain/provider"
	"app/internal/secrets"
)

// Action names.
const (
	ActionSystemPing        = "system:ping"
	ActionSystemEcho        = "system:echo"
	ActionSystemInfo        = "system:info"
	ActionSystemOpenFolder  = "system:open_folder"
	ActionSystemCheckUpdate = "system:check_update"

	ActionPreferencesGet    = "preferences:get"
	ActionPreferencesUpdate = "preferences:update"

	ActionDiagnosticsExport = "diagnostics:export"

	ActionBinariesList     = "binaries:list"
	ActionBinariesProbe    = "binaries:probe"
	ActionBinariesSetPath  = "binaries:set_path"
	ActionBinariesDownload = "binaries:download"

	ActionWorkflowSpecs    = "workflow:specs"
	ActionWorkflowRunNode  = "workflow:run_node"
	ActionWorkflowRunGraph = "workflow:run_graph"

	ActionAgentList               = "agent:list"
	ActionAgentProbe              = "agent:probe"
	ActionAgentSetPath            = "agent:set_path"
	ActionAgentLogin              = "agent:login"
	ActionAgentLogout             = "agent:logout"
	ActionAgentConnect            = "agent:connect"
	ActionAgentDisconnect         = "agent:disconnect"
	ActionAgentSendPrompt         = "agent:send_prompt"
	ActionAgentCancelPrompt       = "agent:cancel_prompt"
	ActionAgentNewConversation    = "agent:new_conversation"
	ActionAgentListConversations  = "agent:list_conversations"
	ActionAgentOpenConversation   = "agent:open_conversation"
	ActionAgentDeleteConversation = "agent:delete_conversation"
	ActionAgentHistory            = "agent:history"
	ActionAgentListApprovals      = "agent:list_approvals"
	ActionAgentResolveApproval    = "agent:resolve_approval"
	ActionAgentSetUIContext       = "agent:set_ui_context"
	ActionAgentListAllowedDirs    = "agent:list_allowed_dirs"
	ActionAgentAddAllowedDir      = "agent:add_allowed_dir"
	ActionAgentRemoveAllowedDir   = "agent:remove_allowed_dir"
	ActionAgentSetModel           = "agent:set_model"
)

// LongActions lists actions whose idle timeout is realtime.long_action_timeout instead of
// realtime.action_timeout, because a single step (one large file, one slow node) may stay
// silent for a while. Like every action they also re-arm the timeout on each progress report
// (see internal/progress). Actions that merely wait on bounded external work (agent sign-in,
// connect, probe) are NOT listed: they keep themselves alive with progress.KeepAlive.
// Server and client derive their timeouts from this single list (the TS copy is generated).
func LongActions() []string {
	return []string{
		ActionBinariesDownload,
		ActionWorkflowRunNode,
		ActionWorkflowRunGraph,
		ActionDiagnosticsExport,
		ActionAgentResolveApproval, // an approved action runs a workflow node or graph
	}
}

// IsLongAction reports whether action is listed in LongActions.
func IsLongAction(action string) bool {
	for _, a := range LongActions() {
		if a == action {
			return true
		}
	}
	return false
}

// Server-pushed event names (WebSocket).
const (
	EventActionProgress     = "action:progress"
	EventPreferencesUpdated = "preferences:updated"
	EventAgentStatusChanged = "agent:status_changed"
	EventAgentChatEvent     = "agent:chat_event"
	EventAgentApprovals     = "agent:approvals"
)

// Native event names emitted through the Wails runtime (desktop shell only).
const (
	NativeEventReady = "app:ready"
	NativeEventFatal = "app:fatal"
)

// ActionSpec declares an action with its payload and result types.
type ActionSpec struct {
	Name    string
	Summary string
	Payload reflect.Type
	Result  reflect.Type
}

// EventSpec declares an event with its data type.
type EventSpec struct {
	Name    string
	Summary string
	Data    reflect.Type
}

// EnumSpec declares a closed set of string values exported as a TypeScript union.
type EnumSpec struct {
	Name   string
	GoType reflect.Type // fields of this Go type are emitted as the enum name
	Values []string
}

// BridgeMethod declares a method bound to the frontend through Wails.
type BridgeMethod struct {
	Name    string
	Summary string
	Params  []reflect.Type
	Result  reflect.Type // nil for no result
}

func t[T any]() reflect.Type { return reflect.TypeFor[T]() }

// Actions is the authoritative action catalogue.
func Actions() []ActionSpec {
	return []ActionSpec{
		{ActionSystemPing, "Round-trip latency probe.", t[Empty](), t[PingResult]()},
		{ActionSystemEcho, "Returns the payload unchanged (diagnostics tool).", t[json.RawMessage](), t[json.RawMessage]()},
		{ActionSystemInfo, "Build, runtime and directory information.", t[Empty](), t[SystemInfo]()},
		{ActionSystemOpenFolder, "Reveals an application folder in the OS file manager.", t[OpenFolderPayload](), t[Empty]()},
		{ActionSystemCheckUpdate, "Checks the release feed for a newer version.", t[Empty](), t[UpdateInfo]()},
		{ActionPreferencesGet, "Returns the current user preferences.", t[Empty](), t[Preferences]()},
		{ActionPreferencesUpdate, "Applies a partial update and returns the result.", t[PreferencesPatch](), t[Preferences]()},
		{ActionDiagnosticsExport, "Writes a diagnostics bundle (reports progress, cancellable).", t[Empty](), t[DiagnosticsBundle]()},
		{ActionBinariesList, "List all managed external binaries and their statuses.", t[Empty](), t[BinaryListResult]()},
		{ActionBinariesProbe, "Probe a specific external binary to verify and extract version.", t[BinaryIDPayload](), t[BinaryInfo]()},
		{ActionBinariesSetPath, "Set or clear custom executable path for a binary.", t[BinarySetPathPayload](), t[BinaryInfo]()},
		{ActionBinariesDownload, "Download and install official static build of binary (cancellable, reports progress).", t[BinaryIDPayload](), t[BinaryInfo]()},
		{ActionWorkflowSpecs, "Returns the catalogue of registered workflow node types.", t[Empty](), t[WorkflowSpecsResult]()},
		{ActionWorkflowRunNode, "Runs one node standalone by type with JSON input.", t[WorkflowRunNodePayload](), t[WorkflowRunNodeResult]()},
		{ActionWorkflowRunGraph, "Runs a full workflow DAG graph in dependency order.", t[WorkflowRunGraphPayload](), t[WorkflowRunGraphResult]()},
		{ActionAgentList, "Lists supported isolated agent providers and their live connection status.", t[Empty](), t[AgentListResult]()},
		{ActionAgentProbe, "Probes an agent provider executable, isolated auth, and version.", t[AgentProviderIDPayload](), t[AgentProviderInfo]()},
		{ActionAgentSetPath, "Sets or clears a custom executable path for an agent provider.", t[AgentSetPathPayload](), t[AgentProviderInfo]()},
		{ActionAgentLogin, "Authenticates the isolated DezuxkStudio profile for an agent provider.", t[AgentLoginPayload](), t[AgentProviderInfo]()},
		{ActionAgentLogout, "Clears isolated DezuxkStudio credentials for an agent provider.", t[AgentProviderIDPayload](), t[AgentProviderInfo]()},
		{ActionAgentConnect, "Starts and initializes an isolated agent connection with DezuxkStudio MCP tools.", t[AgentProviderIDPayload](), t[AgentProviderInfo]()},
		{ActionAgentDisconnect, "Disconnects the active agent connection.", t[Empty](), t[AgentListResult]()},
		{ActionAgentSendPrompt, "Submits a chat prompt to the connected agent.", t[AgentSendPromptPayload](), t[AgentPromptResult]()},
		{ActionAgentCancelPrompt, "Cancels the currently active agent prompt turn.", t[Empty](), t[Empty]()},
		{ActionAgentNewConversation, "Starts a new agent chat; earlier chats stay in history.", t[Empty](), t[Empty]()},
		{ActionAgentHistory, "Returns the current agent chat (empty for a new chat).", t[Empty](), t[AgentHistoryResult]()},
		{ActionAgentListConversations, "Lists earlier agent chats, most recent first.", t[Empty](), t[AgentConversationListResult]()},
		{ActionAgentOpenConversation, "Makes an earlier agent chat current so it can be read and continued.", t[AgentConversationIDPayload](), t[AgentHistoryResult]()},
		{ActionAgentDeleteConversation, "Deletes an agent chat and its messages.", t[AgentConversationIDPayload](), t[Empty]()},
		{ActionAgentListApprovals, "Lists pending high-impact agent actions waiting for desktop UI approval.", t[Empty](), t[AgentApprovalListResult]()},
		{ActionAgentResolveApproval, "Approves or rejects a pending agent action from the desktop UI.", t[AgentResolveApprovalPayload](), t[Empty]()},
		{ActionAgentSetUIContext, "Updates the active UI screen context visible to the agent.", t[AgentUIContextPayload](), t[Empty]()},
		{ActionAgentListAllowedDirs, "Lists user-approved workspace directories accessible to the agent.", t[Empty](), t[AgentAllowedDirsResult]()},
		{ActionAgentAddAllowedDir, "Authorizes a directory for non-code agent file operations.", t[AgentAllowedDirPayload](), t[AgentAllowedDirsResult]()},
		{ActionAgentRemoveAllowedDir, "Revokes agent access to an authorized directory.", t[AgentAllowedDirPayload](), t[AgentAllowedDirsResult]()},
		{ActionAgentSetModel, "Selects the model and reasoning effort used by an agent provider.", t[AgentSetModelPayload](), t[AgentProviderInfo]()},
		{ActionProvidersList, "Lists built-in API providers with their credentials (secrets masked), custom providers and the secret storage backend.", t[Empty](), t[ProvidersListResult]()},
		{ActionProvidersCreateCredential, "Saves a credential for a built-in provider; the first one becomes active.", t[CreateCredentialPayload](), t[ProviderCredential]()},
		{ActionProvidersUpdateCredential, "Updates a credential; empty secrets keep the stored value.", t[UpdateCredentialPayload](), t[ProviderCredential]()},
		{ActionProvidersDeleteCredential, "Deletes a credential and its secrets; the oldest other one becomes active.", t[ProviderIDPayload](), t[Empty]()},
		{ActionProvidersActivate, "Makes a credential the active one of its provider.", t[ProviderIDPayload](), t[Empty]()},
		{ActionProvidersTestCredential, "Checks a saved credential against the provider API.", t[ProviderIDPayload](), t[ProviderTestResult]()},
		{ActionProvidersCreateCustom, "Saves a custom (OpenAI/Gemini/Anthropic-compatible) provider.", t[CustomProviderInput](), t[CustomProvider]()},
		{ActionProvidersUpdateCustom, "Updates a custom provider; an empty api_key keeps the stored key.", t[UpdateCustomPayload](), t[CustomProvider]()},
		{ActionProvidersDeleteCustom, "Deletes a custom provider and its key.", t[ProviderIDPayload](), t[Empty]()},
		{ActionProvidersTestCustom, "Checks a saved custom provider by listing its models.", t[ProviderIDPayload](), t[ProviderTestResult]()},
		{ActionProvidersDiscoverModels, "Lists the models of a (possibly unsaved) custom provider.", t[DiscoverModelsPayload](), t[DiscoverModelsResult]()},
		{ActionProvidersSetModels, "Replaces the model list of a built-in or custom provider (enable, defaults, facts, added ids).", t[SetModelsPayload](), t[ProviderModelsResult]()},
		{ActionProvidersDiscoverBuiltin, "Lists a built-in provider's models with its active key and merges new ones into its model list.", t[ProviderIDPayload](), t[ProviderModelsResult]()},
		{ActionProvidersUpdateSettings, "Patches a provider's concurrency / rate settings; an empty value resets a key.", t[UpdateSettingsPayload](), t[ProviderSettingsResult]()},
		{ActionProvidersDefaults, "Default model per purpose (text, image, video, tts) with the usable candidates.", t[Empty](), t[ProviderDefaultsResult]()},
		{ActionProvidersSetDefault, "Sets the default model of a purpose (provider/model, or empty for auto).", t[SetDefaultPayload](), t[ProviderDefaultsResult]()},
	}
}

// Events is the authoritative WebSocket event catalogue.
func Events() []EventSpec {
	return []EventSpec{
		{EventActionProgress, "Progress of a long-running action; ref_id is the action id.", t[ActionProgress]()},
		{EventPreferencesUpdated, "Preferences changed (from any client).", t[Preferences]()},
		{EventAgentStatusChanged, "Agent provider discovery or connection state changed.", t[AgentProviderInfo]()},
		{EventAgentChatEvent, "Streamed text, thought, tool call, or completion update from the active agent turn.", t[AgentChatEvent]()},
		{EventAgentApprovals, "Pending agent approval queue changed.", t[AgentApprovalListResult]()},
		{EventProvidersChanged, "Provider credentials or custom providers changed.", t[ProvidersListResult]()},
	}
}

// NativeEvents is the Wails runtime event catalogue.
func NativeEvents() []EventSpec {
	return []EventSpec{
		{NativeEventReady, "Core is ready; call GetConnectionInfo.", t[Empty]()},
		{NativeEventFatal, "Core failed to start; the UI must show a fatal screen.", t[ErrorBody]()},
	}
}

// Bridge lists the methods of desktop.Bridge exposed to the frontend.
func Bridge() []BridgeMethod {
	return []BridgeMethod{
		{Name: "GetConnectionInfo", Summary: "Waits for the core and returns how to connect.", Result: t[ConnectionInfo]()},
		{Name: "GetBuildInfo", Summary: "Returns build metadata.", Result: t[BuildInfo]()},
		{Name: "SetRemoteToken", Summary: "Client mode: stores the remote token in the OS credential store.", Params: []reflect.Type{t[string]()}},
		{Name: "BrowseExecutable", Summary: "Opens a native file picker to select an executable file.", Params: []reflect.Type{t[string]()}, Result: t[string]()},
		{Name: "BrowseImage", Summary: "Opens a native file picker to select an image file.", Params: []reflect.Type{t[string]()}, Result: t[string]()},
		{Name: "BrowseImages", Summary: "Opens a native file picker to select multiple image files.", Params: []reflect.Type{t[string]()}, Result: t[[]string]()},
		{Name: "BrowseDirectory", Summary: "Opens a native directory picker to select a folder.", Params: []reflect.Type{t[string]()}, Result: t[string]()},
	}
}

// Enums lists every closed string set exported to TypeScript.
func Enums() []EnumSpec {
	return []EnumSpec{
		{"ErrorCode", t[apperr.Code](), stringsOf(apperr.Codes())},
		{"MessageType", t[MessageType](), MessageTypes()},
		{"Theme", t[preferences.Theme](), stringsOf(preferences.Themes())},
		{"Language", t[preferences.Language](), stringsOf(preferences.Languages())},
		{"FolderTarget", t[FolderTarget](), stringsOf(FolderTargets())},
		{"AppMode", t[config.Mode](), stringsOf(config.Modes())},
		{"BinaryID", t[binaries.BinaryID](), stringsOf(binaries.AllIDs())},
		{"BinaryStatus", t[binaries.Status](), stringsOf([]binaries.Status{binaries.StatusAvailable, binaries.StatusMissing, binaries.StatusInvalid})},
		{"BinarySource", t[binaries.Source](), stringsOf([]binaries.Source{binaries.SourceTools, binaries.SourceCustom, binaries.SourceSystem, binaries.SourceNone})},
		{"AgentProviderID", t[agent.ProviderID](), stringsOf(agent.AllProviderIDs())},
		{"AgentConnectionState", t[agent.ConnectionState](), stringsOf(agent.AllConnectionStates())},
		{"AgentAuthState", t[agent.AuthState](), stringsOf(agent.AllAuthStates())},
		{"AgentSource", t[agent.Source](), stringsOf(agent.AllSources())},
		{"AgentChatEventType", t[agent.ChatEventType](), stringsOf(agent.AllChatEventTypes())},
		{"ProviderCapability", t[provider.Capability](), stringsOf(provider.Capabilities())},
		{"ProviderFieldKind", t[provider.FieldKind](), stringsOf(provider.FieldKinds())},
		{"ProviderDiscoveryFormat", t[provider.DiscoveryFormat](), stringsOf(provider.DiscoveryFormats())},
		{"ProviderEndpoint", t[provider.Endpoint](), endpointKeys()},
		{"ProviderTestOutcome", t[provider.Outcome](), stringsOf(provider.Outcomes())},
		{"ProviderPurpose", t[provider.Purpose](), stringsOf(provider.Purposes())},
		{"SecretBackend", t[secrets.Backend](), stringsOf(secrets.Backends())},
	}
}

// ActionNames returns the declared action names.
func ActionNames() []string {
	specs := Actions()
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

// EventNames returns the declared WebSocket event names.
func EventNames() []string {
	specs := Events()
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

func stringsOf[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

func endpointKeys() []string {
	var out []string
	for _, e := range provider.Endpoints() {
		out = append(out, string(e.Key))
	}
	return out
}
