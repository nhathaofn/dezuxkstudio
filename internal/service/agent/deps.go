package agent

import (
	"context"
	"encoding/json"

	"app/internal/contracts"
	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	"app/internal/engine"
)

// WorkflowRunner exposes registered workflow node specs and execution to DezuxkStudio's MCP server.
type WorkflowRunner interface {
	Specs(ctx context.Context) []engine.Spec
	RunNode(ctx context.Context, nodeType string, input json.RawMessage) (any, error)
	RunGraph(ctx context.Context, g engine.Graph) (engine.GraphResult, error)
}

// BinariesManager exposes external binary inspection and installation to DezuxkStudio's MCP server.
type BinariesManager interface {
	List(ctx context.Context) ([]domainbinaries.BinaryInfo, error)
	Probe(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error)
	Download(ctx context.Context, id domainbinaries.BinaryID) (domainbinaries.BinaryInfo, error)
}

// ManagedCLIs is the app's own, self-updating copy of each agent CLI. It never reads or changes a CLI
// that is installed elsewhere on the machine.
type ManagedCLIs interface {
	Supported(id domainagent.ProviderID) bool
	Current(id domainagent.ProviderID) (domainagent.ManagedCopy, bool)
	// Install makes the newest official release current; changed is false when it already was.
	Install(ctx context.Context, id domainagent.ProviderID) (cur domainagent.ManagedCopy, changed bool, err error)
}

// PreferencesManager exposes app preferences read/update to DezuxkStudio's MCP server.
type PreferencesManager interface {
	Get(ctx context.Context) (domainprefs.Preferences, error)
	Update(ctx context.Context, patch domainprefs.Patch) (domainprefs.Preferences, error)
}

// SystemInspector exposes app runtime/path metadata to DezuxkStudio's MCP server.
type SystemInspector interface {
	Info() contracts.SystemInfo
}

// DiagnosticsExporter exposes diagnostic bundle export to DezuxkStudio's MCP server.
type DiagnosticsExporter interface {
	Export(ctx context.Context) (contracts.DiagnosticsBundle, error)
}
