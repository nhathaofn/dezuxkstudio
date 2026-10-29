package contracts

import (
	"encoding/json"
	"reflect"

	"app/internal/apperr"
	"app/internal/config"
	"app/internal/domain/preferences"
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
)

// Server-pushed event names (WebSocket).
const (
	EventActionProgress     = "action:progress"
	EventPreferencesUpdated = "preferences:updated"
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
	}
}

// Events is the authoritative WebSocket event catalogue.
func Events() []EventSpec {
	return []EventSpec{
		{EventActionProgress, "Progress of a long-running action; ref_id is the action id.", t[ActionProgress]()},
		{EventPreferencesUpdated, "Preferences changed (from any client).", t[Preferences]()},
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
