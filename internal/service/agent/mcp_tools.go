package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
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

// graphNodeToolInput mirrors engine.GraphNode with params as an object: GraphNode.Params is a
// json.RawMessage, which the MCP schema generator types as "null or array" and so rejects every object.
type graphNodeToolInput struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Params map[string]any `json:"params,omitempty" jsonschema:"Same input keys as workflow_run_node"`
}

type runGraphToolInput struct {
	Nodes     []graphNodeToolInput `json:"nodes" jsonschema:"Graph nodes (same input keys as workflow_run_node)"`
	Edges     []engine.Edge        `json:"edges,omitempty"`
	Recursive bool                 `json:"recursive,omitempty" jsonschema:"Include sub-folders"`
}

func (in runGraphToolInput) graphNodes() []engine.GraphNode {
	out := make([]engine.GraphNode, 0, len(in.Nodes))
	for _, n := range in.Nodes {
		var raw json.RawMessage
		if len(n.Params) > 0 {
			raw, _ = json.Marshal(n.Params) // a map[string]any decoded from JSON always re-encodes
		}
		out = append(out, engine.GraphNode{ID: n.ID, Type: n.Type, Params: raw})
	}
	return out
}

type binaryIDToolInput struct {
	ID string `json:"id" jsonschema:"Binary id, e.g. ffmpeg"`
}

type preferencesUpdateToolInput struct {
	Theme    string `json:"theme,omitempty" jsonschema:"system|light|dark"`
	Language string `json:"language,omitempty" jsonschema:"en|vi"`
}

type listFilesToolInput struct {
	Dir        string `json:"dir,omitempty" jsonschema:"Absolute folder path; default: agent workspace"`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"Include files in subfolders"`
	MaxEntries int    `json:"max_entries,omitempty" jsonschema:"Maximum entries to return (default 300, max 1000)"`
}

type readFileToolInput struct {
	Path      string `json:"path" jsonschema:"Text, image, video or audio file path"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"1-based starting line for large files (enables chunked reading past 64 KiB)"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"1-based inclusive ending line (optional)"`
}

type writeFileToolInput struct {
	Path    string `json:"path" jsonschema:"Text file path (.txt .md .json .srt .vtt .csv .xml); relative = agent workspace"`
	Content string `json:"content"`
}

type editFileToolInput struct {
	Path       string `json:"path" jsonschema:"Text file path; relative = agent workspace"`
	Mode       string `json:"mode,omitempty" jsonschema:"replace (default) or append"`
	Content    string `json:"content" jsonschema:"Replacement text or text to append"`
	OldText    string `json:"old_text,omitempty" jsonschema:"Exact text block to replace (when mode=replace)"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"Replace every occurrence of old_text"`
	StartLine  int    `json:"start_line,omitempty" jsonschema:"1-based starting line to replace if old_text is omitted"`
	EndLine    int    `json:"end_line,omitempty" jsonschema:"1-based inclusive ending line to replace"`
}

type searchTextToolInput struct {
	Dir           string `json:"dir,omitempty" jsonschema:"Folder to search; default: agent workspace"`
	Query         string `json:"query" jsonschema:"Text query to search across readable text files"`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"Case-sensitive search (default false)"`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"Max matching lines (default 50, max 200)"`
}

type readDocumentToolInput struct {
	Path        string `json:"path" jsonschema:"Document path (.docx, .odt, .rtf, .pdf)"`
	OffsetChars int    `json:"offset_chars,omitempty" jsonschema:"0-based character offset to start reading"`
	LimitChars  int    `json:"limit_chars,omitempty" jsonschema:"Max characters per chunk (default 16000, max 32000)"`
}

type moveFileToolInput struct {
	Source      string `json:"source"`
	Destination string `json:"destination" jsonschema:"File path or folder; extension must not change"`
	Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"Replace an existing file (needs approval)"`
}

// scrubSecretParams removes the values of a node's secret settings (engine.Param.Secret, e.g. an API key
// the user pasted) from the stored chat history once the node ran: the app keeps them in the credential
// store only.
func (s *Service) scrubSecretParams(ctx context.Context, spec engine.Spec, input map[string]any) {
	for _, p := range spec.Params {
		if !p.Secret {
			continue
		}
		if v, _ := input[p.Name].(string); len(strings.TrimSpace(v)) >= minScrubbedSecret {
			if err := s.repo.RedactChatMessages(ctx, strings.TrimSpace(v), "[secret saved]"); err != nil {
				s.log.Warn("agent: scrub secret from chat history", "error", err)
			}
		}
	}
}

// minScrubbedSecret avoids rewriting history for trivially short values that are not real keys.
const minScrubbedSecret = 8

type deleteFileToolInput struct {
	Path string `json:"path" jsonschema:"File to delete"`
}

// mcpServerInstructions is the short MCP-level reminder (the full rules are the provider's system prompt).
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

// toolDoc is a registered MCP tool's name and description, used to build the prompt's <tools> layer
// and the capabilities snapshot.
type toolDoc struct{ Name, Description string }

// mcpToolset is the MCP server together with the list of the tools registered on it.
type mcpToolset struct {
	server *mcp.Server
	docs   []toolDoc
}

func addMCPTool[I any](ts *mcpToolset, name, description string, fn func(context.Context, I) (*mcp.CallToolResult, any, error)) {
	ts.docs = append(ts.docs, toolDoc{Name: name, Description: description})
	mcp.AddTool(ts.server, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
			return fn(ctx, in)
		})
}

// needsApproval queues an action for the user's decision and returns the standard tool response.
func (s *Service) needsApproval(tool, title, summary string, exec func(context.Context) (any, error)) (*mcp.CallToolResult, any, error) {
	req, err := s.queueApproval(tool, title, summary, exec)
	if err != nil {
		return mcpError(err.Error())
	}
	return mcpJSON(map[string]any{
		"status":      "needs_approval",
		"approval_id": req.ID,
		"summary":     summary,
		"message":     "This action changes the user's files and is waiting for Approve/Reject in the DezuxkStudio panel. Tell the user what will happen and stop; do not claim it is done.",
	})
}

// toolDocs returns the registered MCP tools (static for the service's lifetime, built once).
func (s *Service) toolDocs() []toolDoc {
	s.toolDocsOnce.Do(func() { s.docs = s.buildToolset().docs })
	return s.docs
}

func (s *Service) mcpInstructions() string {
	if s.developerMode {
		return "DezuxkStudio tools (developer mode active); call workflow_list_specs before running an app feature."
	}
	return mcpServerInstructions
}

// buildMCPServer returns a new MCP server with every DezuxkStudio tool registered.
func (s *Service) buildMCPServer() *mcp.Server { return s.buildToolset().server }

func (s *Service) buildToolset() *mcpToolset {
	// Every provider already receives the full profile as its system prompt. MCP clients may copy server
	// instructions into each tool's description (Codex does: 16 tools × ~9k characters ≈ 38k tokens), so
	// the server only states a one-line reminder.
	server := &mcpToolset{server: mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: s.appVersion}, &mcp.ServerOptions{
		Instructions: s.mcpInstructions(),
	})}

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
			run := func(execCtx context.Context) (any, error) {
				defer s.scrubSecretParams(context.WithoutCancel(execCtx), spec, prep.Input)
				return s.workflow.RunNode(execCtx, nodeType, raw)
			}
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
			g, prepared, err := s.prepareGraph(specs, engine.Graph{Nodes: in.graphNodes(), Edges: in.Edges}, in.Recursive)
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
			run := func(execCtx context.Context) (any, error) {
				defer func() {
					for i, p := range prepared {
						spec, _ := specByType(specs, g.Nodes[i].Type)
						s.scrubSecretParams(context.WithoutCancel(execCtx), spec, p.Input)
					}
				}()
				return s.workflow.RunGraph(execCtx, g)
			}
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

	addMCPTool(server, "workspace_list_files",
		"List a folder or subfolder tree (supports recursive=true; protected files are hidden).",
		func(_ context.Context, in listFilesToolInput) (*mcp.CallToolResult, any, error) {
			return s.handleWorkspaceListFiles(in)
		})

	addMCPTool(server, "workspace_read_file",
		"Open a text file (supports start_line/end_line chunked reads past 64 KiB), image, or media metadata.",
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
			switch class := s.policy.Classify(target); class {
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
			return s.handleWorkspaceReadTextSlice(target, info, in.StartLine, in.EndLine)
		})

	addMCPTool(server, "workspace_read_document",
		"Extract paged plain text from .docx, .odt, .rtf, or .pdf documents (offset_chars/limit_chars).",
		func(_ context.Context, in readDocumentToolInput) (*mcp.CallToolResult, any, error) {
			return s.handleWorkspaceReadDocument(in)
		})

	addMCPTool(server, "workspace_search_text",
		"Search for text across readable files in a folder and its subfolders.",
		func(_ context.Context, in searchTextToolInput) (*mcp.CallToolResult, any, error) {
			return s.handleWorkspaceSearchText(in)
		})

	addMCPTool(server, "workspace_write_file",
		"Create or replace a text file. Overwriting outside trusted folders needs approval.",
		func(_ context.Context, in writeFileToolInput) (*mcp.CallToolResult, any, error) {
			target, err := s.policy.ValidateWrite(in.Path)
			if err != nil {
				return mcpError(err.Error())
			}
			if !s.developerMode && ContainsSourceCode(in.Content) {
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

	addMCPTool(server, "workspace_edit_file",
		"Append to or replace a section (old_text or start_line/end_line) in a text file without rewriting the whole file.",
		func(_ context.Context, in editFileToolInput) (*mcp.CallToolResult, any, error) {
			return s.handleWorkspaceEditFile(in)
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
		"tools":             s.toolNames(),
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

	if providers := s.providerSnapshot(ctx); providers != nil {
		appFeatures["providers"] = providers
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
		"developer_mode":        s.developerMode,
		"current_screen":        ui,
		"dezuxkstudio_features": appFeatures,
		"assistant_extras":      assistantExtras,
		// The access rules themselves are in the system prompt; only the folders are data.
		"trusted_folders": s.policy.Roots(),
		"workspace_dir":   s.workspaceDir,
	}
}

// toolNames lists every registered MCP tool, generated from the registrations.
func (s *Service) toolNames() []string {
	docs := s.toolDocs()
	names := make([]string, len(docs))
	for i, d := range docs {
		names[i] = d.Name
	}
	return names
}

func (s *Service) queueApproval(tool, title, summary string, execFn func(context.Context) (any, error)) (domainagent.ApprovalRequest, error) {
	s.mu.Lock()
	pid := s.activeID
	s.mu.Unlock()
	req, ok := s.approvals.queue(pid, tool, title, summary, execFn)
	if !ok {
		max := strconv.Itoa(s.limits.MaxPendingApprovals)
		return req, localize(fmt.Errorf("too many actions are waiting for approval (%s); ask the user to approve or reject them first", max),
			msgTooManyApprovals, map[string]string{"max": max})
	}
	s.emitApprovals()
	return req, nil
}
