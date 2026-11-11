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
	"unicode/utf8"

	domainagent "app/internal/domain/agent"
	domainbinaries "app/internal/domain/binaries"
	domainprefs "app/internal/domain/preferences"
	"app/internal/engine"
	"app/internal/fsutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxWorkspaceReadBytes = 256 << 10
	maxImageReadBytes     = 5 << 20
	maxListEntries        = 2000
)

type runNodeToolInput struct {
	Type      string         `json:"type" jsonschema:"Registered node type from workflow_list_specs (e.g. tool.watermark_remover)"`
	Input     map[string]any `json:"input" jsonschema:"Object whose keys are the node's input port names and param names from workflow_list_specs. Media ports take file paths, folder paths, or lists of them; folders are expanded by DezuxkStudio. Omitted params use the same defaults as the feature's UI."`
	Recursive bool           `json:"recursive,omitempty" jsonschema:"Also include files in sub-folders when a folder path is given"`
}

type runGraphToolInput struct {
	Nodes     []engine.GraphNode `json:"nodes" jsonschema:"Workflow graph nodes; params use the same keys as workflow_run_node input"`
	Edges     []engine.Edge      `json:"edges,omitempty" jsonschema:"Workflow graph edges"`
	Recursive bool               `json:"recursive,omitempty" jsonschema:"Also include files in sub-folders when a folder path is given"`
}

type binaryIDToolInput struct {
	ID string `json:"id" jsonschema:"Binary identifier from binaries_list (e.g. ffmpeg)"`
}

type preferencesUpdateToolInput struct {
	Theme    string `json:"theme,omitempty" jsonschema:"Optional theme: system, light, or dark"`
	Language string `json:"language,omitempty" jsonschema:"Optional language: en or vi"`
}

type listFilesToolInput struct {
	Dir string `json:"dir,omitempty" jsonschema:"Absolute folder path anywhere on the user's computer (e.g. D:\\Photos); defaults to the DezuxkStudio agent workspace"`
}

type readFileToolInput struct {
	Path string `json:"path" jsonschema:"Absolute path of a text file (.txt, .md, .json, .srt, .vtt, .csv, …) or an image/video/audio file"`
}

type writeFileToolInput struct {
	Path    string `json:"path" jsonschema:"Target text file path (.txt, .md, .json, .srt, .vtt, .csv, .xml); relative paths go to the agent workspace"`
	Content string `json:"content" jsonschema:"UTF-8 text content to write"`
}

type moveFileToolInput struct {
	Source      string `json:"source" jsonschema:"Existing file path"`
	Destination string `json:"destination" jsonschema:"Destination file path or existing folder; the file type (extension) must stay the same"`
	Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"Set true only if replacing an existing destination file (requires user approval)"`
}

type deleteFileToolInput struct {
	Path string `json:"path" jsonschema:"File to delete; outside DezuxkStudio's own folders this requires user approval"`
}

func mcpJSON(data any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: data,
	}, nil, nil
}

func mcpError(msg string) (*mcp.CallToolResult, any, error) {
	payload := map[string]any{"ok": false, "error": msg}
	b, _ := json.Marshal(payload)
	return &mcp.CallToolResult{
		IsError:           true,
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: payload,
	}, nil, nil
}

func addMCPTool[I any](server *mcp.Server, name, description string, fn func(context.Context, I) (*mcp.CallToolResult, any, error)) {
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

func (s *Service) buildMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: s.appVersion}, &mcp.ServerOptions{
		Instructions: SystemInstructions(),
	})

	addMCPTool(server, "studio_list_capabilities",
		"List all live features, registered workflow nodes, binaries, preferences, UI context, and trusted folders currently available in DezuxkStudio.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			return mcpJSON(s.buildCapabilitiesSnapshot(ctx))
		})

	addMCPTool(server, "workflow_list_specs",
		"List DezuxkStudio's features as workflow nodes: input ports (with accepted media types), params (with allowed options and the UI's default values), and outputs. Always read this before running a feature.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.workflow == nil {
				return mcpError("workflow service is unavailable")
			}
			return mcpJSON(map[string]any{"specs": s.workflow.Specs(ctx)})
		})

	addMCPTool(server, "workflow_run_node",
		"Run one DezuxkStudio feature (workflow node) exactly like its UI does. Pass files or folders; DezuxkStudio expands folders, applies the UI defaults, validates settings, and returns the real per-file results. Settings that overwrite original files require user approval.",
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
		"Run a multi-step DezuxkStudio workflow (DAG of nodes). Each node is validated like workflow_run_node; graphs with destructive settings require user approval.",
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
		"List external binaries managed by DezuxkStudio and their availability status.",
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

	addMCPTool(server, "binaries_probe",
		"Probe one managed binary in DezuxkStudio.",
		func(ctx context.Context, in binaryIDToolInput) (*mcp.CallToolResult, any, error) {
			if s.binaries == nil {
				return mcpError("binaries service is unavailable")
			}
			info, err := s.binaries.Probe(ctx, domainbinaries.BinaryID(strings.TrimSpace(in.ID)))
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(info)
		})

	addMCPTool(server, "binaries_download",
		"Propose downloading an official managed binary into DezuxkStudio tools directory (always requires UI approval).",
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
				func(execCtx context.Context) (any, error) { return s.binaries.Download(execCtx, id, nil) })
		})

	addMCPTool(server, "preferences_get",
		"Get current DezuxkStudio user preferences (theme and language).",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.preferences == nil {
				return mcpError("preferences service is unavailable")
			}
			prefs, err := s.preferences.Get(ctx)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(prefs)
		})

	addMCPTool(server, "preferences_update",
		"Update DezuxkStudio preferences (theme: system|light|dark, language: en|vi).",
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

	addMCPTool(server, "system_info",
		"Read DezuxkStudio build, runtime, and non-sensitive application directory metadata.",
		func(_ context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.system == nil {
				return mcpError("system service is unavailable")
			}
			return mcpJSON(s.system.Info())
		})

	addMCPTool(server, "diagnostics_export",
		"Export a DezuxkStudio diagnostics zip bundle for troubleshooting.",
		func(ctx context.Context, _ struct{}) (*mcp.CallToolResult, any, error) {
			if s.diagnostics == nil {
				return mcpError("diagnostics service is unavailable")
			}
			bundle, err := s.diagnostics.Export(ctx, nil)
			if err != nil {
				return mcpError(err.Error())
			}
			return mcpJSON(bundle)
		})

	addMCPTool(server, "workspace_list_files",
		"List files and folders anywhere on the user's computer (system folders, credentials, and source code are hidden). Each entry has its file class (text, image, video, audio, document, other).",
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
		"Open a file: text files return their content; images are returned as images you can look at; video/audio return metadata (use DezuxkStudio features to process them).",
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
		"Create or update a text content file (.txt, .md, .json, .srt, .vtt, .csv, .xml) anywhere on the user's computer. Overwriting an existing file outside DezuxkStudio's folders requires user approval.",
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
		"Move or rename a file anywhere on the user's computer, keeping its file type. Replacing an existing destination requires user approval.",
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
		"Delete a file. Inside DezuxkStudio's own folders it is deleted directly; anywhere else it requires user approval.",
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
		"note":              "Real DezuxkStudio features. Run them with workflow_run_node (call workflow_list_specs for full input/setting details).",
		"workflow_features": features,
		"system_features": []string{
			"Managed external tools (binaries_list / binaries_probe / binaries_download), e.g. ffmpeg",
			"App preferences: theme and language (preferences_get / preferences_update)",
			"System information and diagnostics export (system_info / diagnostics_export)",
		},
	}
	if s.binaries != nil {
		if bins, err := s.binaries.List(ctx); err == nil {
			appFeatures["binaries"] = bins
		}
	}
	if s.preferences != nil {
		if prefs, err := s.preferences.Get(ctx); err == nil {
			appFeatures["preferences"] = prefs
		}
	}

	// Group 2: what the assistant does by itself. These are NOT DezuxkStudio features and must never be presented as such.
	assistantExtras := map[string]any{
		"note": "NOT DezuxkStudio features: the assistant does these itself, in chat or with its file tools, to support the app's work.",
		"items": []string{
			"Write video scripts, storyboards, voice-over text, captions",
			"Translate natural-language text and subtitles",
			"Write and refine prompts for image/video/AI generation",
			"Analyze documents, images, subtitles, and media metadata",
			"Organize the user's files: list, read, create text files, move/rename, delete (with approval outside trusted folders)",
		},
	}

	return map[string]any{
		"app":                   clientName,
		"version":               s.appVersion,
		"current_screen":        ui,
		"dezuxkstudio_features": appFeatures,
		"assistant_extras":      assistantExtras,
		"file_access": map[string]any{
			"scope":         "Any folder on the user's computer except system folders, credentials, and source code. Overwrite/delete outside trusted folders needs approval.",
			"trusted_roots": s.policy.Roots(),
			"workspace_dir": s.workspaceDir,
			"media_dir":     s.mediaDir,
		},
		"never": []string{"read or write source code", "run terminal/OS commands", "control the computer or other apps", "switch DezuxkStudio screens (no tool for that yet)"},
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
