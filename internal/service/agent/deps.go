package agent

import (
	"context"
	"encoding/json"

	"app/internal/contracts"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	domainprovider "app/internal/domain/provider"
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

// PreferencesManager exposes app preferences read/update to DezuxkStudio's MCP server.
type PreferencesManager interface {
	Get(ctx context.Context) (domainprefs.Preferences, error)
	Update(ctx context.Context, patch domainprefs.Patch) (domainprefs.Preferences, error)
}

// SystemInspector exposes app runtime/path metadata to DezuxkStudio's MCP server.
type SystemInspector interface {
	Info() contracts.SystemInfo
}

// ProvidersManager exposes API provider setup to DezuxkStudio's MCP server. Results are JSON-ready and
// never contain a secret value.
type ProvidersManager interface {
	AgentOverview(ctx context.Context) (any, error)
	PlanSetup(req domainprovider.SetupRequest) (domainprovider.SetupPlan, error)
	AgentSetup(ctx context.Context, req domainprovider.SetupRequest) (any, error)
	AgentTest(ctx context.Context, id string) (any, error)
	ActivateCredential(ctx context.Context, id string) error
	AgentDelete(ctx context.Context, id string) error
	Describe(ctx context.Context, id string) (string, error)
}

// DiagnosticsExporter exposes diagnostic bundle export to DezuxkStudio's MCP server.
type DiagnosticsExporter interface {
	Export(ctx context.Context) (contracts.DiagnosticsBundle, error)
}
