package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	domainprovider "app/internal/domain/provider"
	"app/internal/engine"
	"app/internal/fsutil"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// Tool output stays in the model's context for the rest of the conversation, so results are capped.
	maxWorkspaceReadBytes = 64 << 10 // ~16k tokens of text
	maxImageReadBytes     = 5 << 20
	maxListEntries        = 300
)

type runNodeToolInput struct {
	Type      string         `json:"type" jsonschema:"Node type from workflow_list_specs"`
	Input     map[string]any `json:"input" jsonschema:"Input ports and params by name; media ports take file/folder paths or lists; omitted params use UI defaults"`
	Recursive bool           `json:"recursive,omitempty" jsonschema:"Include sub-folders"`
}

type runGraphToolInput struct {
	Nodes     []engine.GraphNode `json:"nodes" jsonschema:"Graph nodes (same input keys as workflow_run_node)"`
	Edges     []engine.Edge      `json:"edges,omitempty"`
	Recursive bool               `json:"recursive,omitempty" jsonschema:"Include sub-folders"`
}

type binaryIDToolInput struct {
	ID string `json:"id" jsonschema:"Binary id, e.g. ffmpeg"`
}

type preferencesUpdateToolInput struct {
	Theme    string `json:"theme,omitempty" jsonschema:"system|light|dark"`
	Language string `json:"language,omitempty" jsonschema:"en|vi"`
}

type listFilesToolInput struct {
	Dir string `json:"dir,omitempty" jsonschema:"Absolute folder path; default: agent workspace"`
}

type readFileToolInput struct {
	Path string `json:"path" jsonschema:"Text, image, video or audio file path"`
}

type writeFileToolInput struct {
	Path    string `json:"path" jsonschema:"Text file path (.txt .md .json .srt .vtt .csv .xml); relative = agent workspace"`
	Content string `json:"content"`
}

type moveFileToolInput struct {
	Source      string `json:"source"`
	Destination string `json:"destination" jsonschema:"File path or folder; extension must not change"`
	Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"Replace an existing file (needs approval)"`
}

type providersSetupToolInput struct {
	ProviderID      string            `json:"provider_id,omitempty" jsonschema:"Built-in id from providers_list; omit to detect from base_url/key"`
	APIKey          string            `json:"api_key,omitempty"`
	BaseURL         string            `json:"base_url,omitempty" jsonschema:"API base URL; a non built-in host becomes a custom provider"`
	Name            string            `json:"name,omitempty"`
	DiscoveryFormat string            `json:"discovery_format,omitempty" jsonschema:"Custom only: openai|gemini|anthropic (guessed when empty)"`
	Secrets         map[string]string `json:"secrets,omitempty" jsonschema:"Other secret fields, e.g. access_key+secret_key (Kling), credentials_file JSON (Vertex)"`
	Values          map[string]string `json:"values,omitempty" jsonschema:"Other non-secret fields, e.g. gcs_bucket"`
}

func (in providersSetupToolInput) request() domainprovider.SetupRequest {
	return domainprovider.SetupRequest{ProviderID: in.ProviderID, APIKey: in.APIKey, BaseURL: in.BaseURL, Name: in.Name,
		DiscoveryFormat: domainprovider.DiscoveryFormat(strings.TrimSpace(in.DiscoveryFormat)), Secrets: in.Secrets, Values: in.Values}
}

type providerIDToolInput struct {
	ID string `json:"id" jsonschema:"Credential id or custom provider id from providers_list"`
}

// redactSecrets removes keys the user pasted in chat from the stored chat history once they are saved
// in the credential store (secrets must not stay in the database).
func (s *Service) redactSecrets(ctx context.Context, req domainprovider.SetupRequest) {
	vals := []string{req.APIKey}
	for _, v := range req.Secrets {
		vals = append(vals, v)
	}
	for _, v := range vals {
		if v = strings.TrimSpace(v); len(v) >= 8 {
			if err := s.repo.RedactChatMessages(ctx, v, "[secret saved]"); err != nil {
				s.log.Warn("agent: redact chat secret", "error", err)
			}
		}
	}
}

type deleteFileToolInput struct {
	Path string `json:"path" jsonschema:"File to delete"`
}

// mcpServerInstructions is the short MCP-level reminder (the full rules are the provider's system prompt).
var toolDocsOnce sync.Once

const mcpServerInstructions = "DezuxkStudio's own tools. Follow the DezuxkStudio Assistant rules from your system prompt; " +
	"call workflow_list_specs before running a feature."

func mcpJSON(data any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, nil, err
	}
	// Text only: some clients pass both text and structuredContent to the model, doubling every result.
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

func mcpError(msg string) (*mcp.CallToolResult, any, error) {
	payload := map[string]any{"ok": false, "error": msg}
	b, _ := json.Marshal(payload)
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// toolDoc is a registered MCP tool's name and description, used to build the prompt's <tools> layer.
type toolDoc struct{ Name, Description string }

// mcpToolDocs lists the tools registered by buildMCPServer, in registration order.
var mcpToolDocs []toolDoc

func addMCPTool[I any](server *mcp.Server, name, description string, fn func(context.Context, I) (*mcp.CallToolResult, any, error)) {
	recordToolDoc(name, description)
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
			return fn(ctx, in)
		})
}

// needsApproval queues an action for the user's decision and returns the standard tool response.
func (s *Service) needsApproval(tool, title, summary string, exec func(context.Context) (any, error)) (*mcp.CallToolResult, any, error) {
	req := s.queueApproval(tool, title, summary, exec)
	return mcpJSON(map[string]any{
		"status":      "needs_approval",
		"approval_id": req.ID,
		"summary":     summary,
		"message":     "This action changes the user's files and is waiting for Approve/Reject in the DezuxkStudio panel. Tell the user what will happen and stop; do not claim it is done.",
	})
}

func recordToolDoc(name, description string) {
	for _, d := range mcpToolDocs {
		if d.Name == name {
			return
		}
	}
	mcpToolDocs = append(mcpToolDocs, toolDoc{Name: name, Description: description})
}

// registeredToolDocs builds the MCP server once (tools are static) and returns its tool list.
func (s *Service) registeredToolDocs() []toolDoc {
	toolDocsOnce.Do(func() { _ = s.buildMCPServer() })
	return mcpToolDocs
}

func (s *Service) buildMCPServer() *mcp.Server {
	// Every provider already receives the full profile as its system prompt. MCP clients may copy server
	// instructions into each tool's description (Codex does: 16 tools × ~9k characters ≈ 38k tokens), so
	// the server only states a one-line reminder.
	server := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: s.appVersion}, &mcp.ServerOptions{
		Instructions: mcpServerInstructions,
	})

	addMCPTool(server, "studio_list_capabilities",
		"App features, managed tools, preferences, current screen and trusted folders.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			return mcpJSON(s.buildCapabilitiesSnapshot(ctx))
		})

	addMCPTool(server, "workflow_list_specs",
		"Inputs, settings (options, defaults) and outputs of every app feature. Read before running one.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.workflow == nil {
				return mcpError("workflow service is unavailable")
			}
			return mcpJSON(map[string]any{"specs": s.workflow.Specs(ctx)})
		})

	addMCPTool(server, "workflow_run_node",
		"Run one app feature like its screen does. Destructive settings wait for user approval.",
		func(ctx context.Context, in runNodeToolInput) (*mcp.CallToolResult, any, error) {
			if s.workflow == nil {
				return mcpError("workflow service is unavailable")
			}
			nodeType := strings.TrimSpace(in.Type)
			spec, ok := specByType(s.workflow.Specs(ctx), nodeType)
			if !ok {
				return mcpError(fmt.Sprintf("unknown node type %q; call workflow_list_specs for the available features", nodeType))
			}
			prep, err := s.prepareNodeInput(spec, in.Input, nil, in.Recursive)
			if err != nil {
				return mcpError(err.Error())
			}
			raw, err := json.Marshal(prep.Input)
			if err != nil {
				return mcpError(fmt.Sprintf("invalid input: %v", err))
			}
			run := func(execCtx context.Context) (any, error) { return s.workflow.RunNode(execCtx, nodeType, raw) }
			if len(prep.Destructive) > 0 {
				return s.needsApproval("workflow_run_node", "Run "+spec.Label, prep.summary(spec), run)
			}
			res, err := run(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(map[string]any{"ok": true, "ran": prep.summary(spec), "input_files": len(prep.Files), "defaults_applied": prep.Defaults, "result": res})
		})

	addMCPTool(server, "workflow_run_graph",
		"Run several features as a workflow (DAG). Destructive settings wait for user approval.",
		func(ctx context.Context, in runGraphToolInput) (*mcp.CallToolResult, any, error) {
			if s.workflow == nil {
				return mcpError("workflow service is unavailable")
			}
			specs := s.workflow.Specs(ctx)
			g, prepared, err := s.prepareGraph(specs, engine.Graph{Nodes: in.Nodes, Edges: in.Edges}, in.Recursive)
			if err != nil {
				return mcpError(err.Error())
			}
			var parts []string
			destructive := false
			for i, p := range prepared {
				spec, _ := specByType(specs, g.Nodes[i].Type)
				parts = append(parts, p.summary(spec))
				destructive = destructive || len(p.Destructive) > 0
			}
			summary := fmt.Sprintf("Workflow (%d steps): %s", len(g.Nodes), strings.Join(parts, " → "))
			run := func(execCtx context.Context) (any, error) { return s.workflow.RunGraph(execCtx, g) }
			if destructive {
				return s.needsApproval("workflow_run_graph", "Run workflow", summary, run)
			}
			res, err := run(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(map[string]any{"ok": true, "ran": summary, "result": res})
		})

	addMCPTool(server, "binaries_list",
		"Managed external tools (e.g. ffmpeg) with status and version.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.binaries == nil {
				return mcpError("binaries service is unavailable")
			}
			items, err := s.binaries.List(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(map[string]any{"binaries": items})
		})

	addMCPTool(server, "binaries_download",
		"Propose downloading a managed tool (needs user approval).",
		func(_ context.Context, in binaryIDToolInput) (*mcp.CallToolResult, any, error) {
			if s.binaries == nil {
				return mcpError("binaries service is unavailable")
			}
			id := domainbinaries.BinaryID(strings.TrimSpace(in.ID))
			if err := domainbinaries.ValidateID(id); err != nil {
				return mcpError(err.Error())
			}
			return s.needsApproval("binaries_download",
				fmt.Sprintf("Download binary %s", id),
				fmt.Sprintf("Download and install official binary %q into DezuxkStudio tools directory.", id),
				func(execCtx context.Context) (any, error) { return s.binaries.Download(execCtx, id) })
		})

	addMCPTool(server, "preferences_update",
		"Change theme (system|light|dark) or language (en|vi).",
		func(ctx context.Context, in preferencesUpdateToolInput) (*mcp.CallToolResult, any, error) {
			if s.preferences == nil {
				return mcpError("preferences service is unavailable")
			}
			var patch domainprefs.Patch
			if t := strings.TrimSpace(in.Theme); t != "" {
				th := domainprefs.Theme(t)
				patch.Theme = &th
			}
			if l := strings.TrimSpace(in.Language); l != "" {
				lang := domainprefs.Language(l)
				patch.Language = &lang
			}
			updated, err := s.preferences.Update(ctx, patch)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(updated)
		})

	addMCPTool(server, "diagnostics_export",
		"Export a diagnostics bundle for troubleshooting.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.diagnostics == nil {
				return mcpError("diagnostics service is unavailable")
			}
			bundle, err := s.diagnostics.Export(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(bundle)
		})

	addMCPTool(server, "providers_list",
		"API providers (LLM/image/video/TTS): which are configured, what fields each needs, saved credentials (masked).",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.providers == nil {
				return mcpError("providers service is unavailable")
			}
			out, err := s.providers.AgentOverview(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(out)
		})

	addMCPTool(server, "providers_setup",
		"Save an API key the user gave you: built-in provider (detected from base_url/key, or provider_id) or a custom "+
			"OpenAI/Gemini/Anthropic-compatible endpoint (models auto-discovered). Activates and tests it. Never repeat the key back.",
		func(ctx context.Context, in providersSetupToolInput) (*mcp.CallToolResult, any, error) {
			if s.providers == nil {
				return mcpError("providers service is unavailable")
			}
			req := in.request()
			out, err := s.providers.AgentSetup(ctx, req)
			if err != nil {
				return mcpError(err.Error())
			}
			s.redactSecrets(ctx, req)
			return mcpJSON(out)
		})

	addMCPTool(server, "providers_test",
		"Test a saved provider credential or custom provider by id (from providers_list).",
		func(ctx context.Context, in providerIDToolInput) (*mcp.CallToolResult, any, error) {
			if s.providers == nil {
				return mcpError("providers service is unavailable")
			}
			out, err := s.providers.AgentTest(ctx, strings.TrimSpace(in.ID))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(out)
		})

	addMCPTool(server, "providers_activate",
		"Make a saved credential the active one of its provider.",
		func(ctx context.Context, in providerIDToolInput) (*mcp.CallToolResult, any, error) {
			if s.providers == nil {
				return mcpError("providers service is unavailable")
			}
			if err := s.providers.ActivateCredential(ctx, strings.TrimSpace(in.ID)); err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(map[string]any{"ok": true})
		})

	addMCPTool(server, "providers_delete",
		"Propose deleting a provider credential or custom provider (needs user approval).",
		func(ctx context.Context, in providerIDToolInput) (*mcp.CallToolResult, any, error) {
			if s.providers == nil {
				return mcpError("providers service is unavailable")
			}
			id := strings.TrimSpace(in.ID)
			name, err := s.providers.Describe(ctx, id)
			if err != nil {
				return mcpError(err.Error())
			}
			return s.needsApproval("providers_delete", "Delete provider credential "+name,
				fmt.Sprintf("Delete %s and its stored API key.", name),
				func(execCtx context.Context) (any, error) {
					return map[string]any{"deleted": name}, s.providers.AgentDelete(execCtx, id)
				})
		})

	addMCPTool(server, "workspace_list_files",
		"List a folder (system, credential and code files are hidden).",
		func(_ context.Context, in listFilesToolInput) (*mcp.CallToolResult, any, error) {
			target, err := s.policy.ValidateDir(in.Dir)
			if err != nil {
				return mcpError(err.Error())
			}
			entries, err := os.ReadDir(target)
			if err != nil {
				return mcpError(err.Error())
			}
			type item struct {
				Name  string    `json:"name"`
				Path  string    `json:"path"`
				IsDir bool      `json:"is_dir"`
				Class FileClass `json:"class,omitempty"`
				Size  int64     `json:"size,omitempty"`
			}
			out := make([]item, 0, len(entries))
			counts := map[FileClass]int{}
			truncated := false
			for _, e := range entries {
				full := filepath.Join(target, e.Name())
				if !s.policy.VisibleInListing(full, e.IsDir()) {
					continue
				}
				it := item{Name: e.Name(), Path: full, IsDir: e.IsDir()}
				if !e.IsDir() {
					it.Class = ClassifyFile(full)
					counts[it.Class]++
					if info, statErr := e.Info(); statErr == nil {
						it.Size = info.Size()
					}
				}
				if len(out) >= maxListEntries {
					truncated = true
					continue
				}
				out = append(out, it)
			}
			sort.Slice(out, func(i, j int) bool {
				if out[i].IsDir != out[j].IsDir {
					return out[i].IsDir
				}
				return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
			})
			return mcpJSON(map[string]any{"dir": target, "trusted": s.policy.IsTrusted(target), "counts": counts, "truncated": truncated, "entries": out})
		})

	addMCPTool(server, "workspace_read_file",
		"Open a text file (content), image (view) or video/audio (metadata).",
		func(_ context.Context, in readFileToolInput) (*mcp.CallToolResult, any, error) {
			target, err := s.policy.ValidateRead(in.Path)
			if err != nil {
				return mcpError(err.Error())
			}
			info, err := os.Stat(target)
			if err != nil {
				return mcpError(err.Error())
			}
			if info.IsDir() {
				return mcpError("target path is a folder; use workspace_list_files")
			}
			switch class := ClassifyFile(target); class {
			case FileClassImage:
				if info.Size() > maxImageReadBytes {
					return mcpJSON(map[string]any{"path": target, "class": class, "size": info.Size(), "note": "image too large to display; metadata only"})
				}
				b, err := os.ReadFile(target)
				if err != nil {
					return mcpError(err.Error())
				}
				mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(target)))
				meta, _ := json.Marshal(map[string]any{"path": target, "class": class, "size": info.Size()})
				return &mcp.CallToolResult{Content: []mcp.Content{
					&mcp.TextContent{Text: string(meta)},
					&mcp.ImageContent{Data: b, MIMEType: mimeType},
				}}, nil, nil
			case FileClassVideo, FileClassAudio:
				return mcpJSON(map[string]any{"path": target, "class": class, "size": info.Size(), "modified": info.ModTime()})
			}
			if info.Size() > maxWorkspaceReadBytes {
				return mcpError(fmt.Sprintf("file exceeds maximum readable size (%d bytes)", maxWorkspaceReadBytes))
			}
			b, err := os.ReadFile(target)
			if err != nil {
				return mcpError(err.Error())
			}
			if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
				return mcpError("file is not a UTF-8 text file")
			}
			content := string(b)
			if ContainsSourceCode(content) {
				return mcpError("refused: this file contains source code or terminal commands, which DezuxkStudio Assistant does not work with")
			}
			return mcpJSON(map[string]any{"path": target, "size": len(b), "content": content})
		})

	addMCPTool(server, "workspace_write_file",
		"Create or update a text file. Overwriting outside trusted folders needs approval.",
		func(_ context.Context, in writeFileToolInput) (*mcp.CallToolResult, any, error) {
			target, err := s.policy.ValidateWrite(in.Path)
			if err != nil {
				return mcpError(err.Error())
			}
			if ContainsSourceCode(in.Content) {
				s.log.Warn("agent: blocked workspace_write_file containing source code", "path", target)
				return mcpError("refused: content contains source code or terminal commands; DezuxkStudio Assistant only writes non-code content (scripts for videos, prompts, subtitles, notes, data)")
			}
			content := in.Content
			write := func(context.Context) (any, error) {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return nil, err
				}
				if err := fsutil.WriteFileAtomic(target, []byte(content), 0o644); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "path": target, "bytes": len(content)}, nil
			}
			if _, statErr := os.Stat(target); statErr == nil && !s.policy.IsTrusted(target) {
				return s.needsApproval("workspace_write_file", "Overwrite "+filepath.Base(target),
					fmt.Sprintf("Replace the existing file %s (%d bytes of new content).", target, len(content)), write)
			}
			res, err := write(context.Background())
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "workspace_move_file",
		"Move or rename a file, keeping its type. Replacing a file needs approval.",
		func(_ context.Context, in moveFileToolInput) (*mcp.CallToolResult, any, error) {
			src, dst, err := s.policy.ValidateMove(in.Source, in.Destination)
			if err != nil {
				return mcpError(err.Error())
			}
			st, err := os.Stat(src)
			if err != nil {
				return mcpError(err.Error())
			}
			if st.IsDir() {
				return mcpError("moving whole folders is not supported; move the files inside it")
			}
			move := func(context.Context) (any, error) {
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return nil, err
				}
				if err := os.Rename(src, dst); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "source": src, "destination": dst}, nil
			}
			if _, err := os.Stat(dst); err == nil {
				if !in.Overwrite {
					return mcpError(fmt.Sprintf("destination %q already exists (set overwrite=true to replace it)", dst))
				}
				return s.needsApproval("workspace_move_file", "Replace "+filepath.Base(dst),
					fmt.Sprintf("Move %s to %s, replacing the existing file.", src, dst), move)
			}
			res, err := move(context.Background())
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	addMCPTool(server, "workspace_delete_file",
		"Delete a file. Outside trusted folders this needs approval.",
		func(_ context.Context, in deleteFileToolInput) (*mcp.CallToolResult, any, error) {
			target, err := s.policy.ValidateDelete(in.Path)
			if err != nil {
				return mcpError(err.Error())
			}
			info, err := os.Stat(target)
			if err != nil {
				return mcpError(err.Error())
			}
			if info.IsDir() {
				return mcpError("deleting folders is not allowed")
			}
			del := func(context.Context) (any, error) {
				if err := os.Remove(target); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "deleted": target}, nil
			}
			if !s.policy.IsTrusted(target) {
				return s.needsApproval("workspace_delete_file", "Delete "+filepath.Base(target),
					fmt.Sprintf("Permanently delete %s (%d bytes).", target, info.Size()), del)
			}
			res, err := del(context.Background())
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(res)
		})

	return server
}

func (s *Service) buildCapabilitiesSnapshot(ctx context.Context) map[string]any {
	s.mu.Lock()
	ui := s.uiContext.normalized()
	s.mu.Unlock()

	// Group 1: what DezuxkStudio itself can do (real features, read live from the app).
	type featureSummary struct {
		Type        string   `json:"type"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Inputs      []string `json:"inputs,omitempty"`
		Settings    []string `json:"settings,omitempty"`
	}
	var features []featureSummary
	if s.workflow != nil {
		for _, sp := range s.workflow.Specs(ctx) {
			f := featureSummary{Type: sp.Type, Name: sp.Label, Description: sp.Description}
			for _, p := range sp.Inputs {
				f.Inputs = append(f.Inputs, fmt.Sprintf("%s (%s)", p.Name, p.Type))
			}
			for _, p := range sp.Params {
				f.Settings = append(f.Settings, p.Name)
			}
			features = append(features, f)
		}
	}
	appFeatures := map[string]any{
		"note":              "App features; details in workflow_list_specs.",
		"workflow_features": features,
		"system_features":   []string{"managed tools (binaries_*)", "theme/language (preferences_update)", "diagnostics_export", "API providers / keys (providers_*)"},
	}
	if s.binaries != nil {
		if bins, err := s.binaries.List(ctx); err == nil {
			// Only what the agent needs to decide; paths and descriptions are in binaries_list.
			type binarySummary struct {
				ID          domainbinaries.BinaryID `json:"id"`
				Status      domainbinaries.Status   `json:"status"`
				Version     string                  `json:"version,omitempty"`
				CanDownload bool                    `json:"can_download,omitempty"`
			}
			summary := make([]binarySummary, 0, len(bins))
			for _, b := range bins {
				summary = append(summary, binarySummary{ID: b.ID, Status: b.Status, Version: b.Version, CanDownload: b.CanDownload})
			}
			appFeatures["binaries"] = summary
		}
	}
	if s.preferences != nil {
		if prefs, err := s.preferences.Get(ctx); err == nil {
			appFeatures["preferences"] = prefs
		}
	}

	// Group 2: what the assistant does by itself. These are NOT DezuxkStudio features and must never be presented as such.
	assistantExtras := map[string]any{
		"note": "NOT DezuxkStudio features: the assistant does these itself, in the chat or with its file tools.",
		"items": []string{
			"Write video scripts, storyboards, voice-over text and captions",
			"Translate text and subtitles between human languages",
			"Write and refine prompts for image, video and AI generation",
			"Analyze documents, images, subtitles and media metadata",
			"Organize files: list, read, create text files, move/rename, delete (with approval outside trusted folders)",
		},
	}

	return map[string]any{
		"app":                   clientName,
		"version":               s.appVersion,
		"current_screen":        ui,
		"dezuxkstudio_features": appFeatures,
		"assistant_extras":      assistantExtras,
		// The access rules themselves are in the system prompt; only the folders are data.
		"trusted_folders": s.policy.Roots(),
		"workspace_dir":   s.workspaceDir,
	}
}

func (s *Service) queueApproval(tool, title, summary string, execFn func(context.Context) (any, error)) domainagent.ApprovalRequest {
	s.mu.Lock()
	pid := s.activeID
	s.mu.Unlock()
	req := s.approvals.queue(pid, tool, title, summary, execFn)
	s.emitApprovals()
	return req
}
