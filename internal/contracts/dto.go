package contracts

import (
	"encoding/json"
	"fmt"
	"slices"

	"app/internal/apperr"
	"app/internal/buildinfo"
	"app/internal/config"
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
	return Preferences{Theme: p.Theme, Language: p.Language}
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
	return preferences.Patch{Theme: p.Theme, Language: p.Language}
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
