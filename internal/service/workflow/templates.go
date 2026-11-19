package workflow

import (
	"fmt"
	"strings"

	"app/internal/contracts"
	"app/internal/engine"
)

func buildStudioTemplates(reg *engine.Registry) ([]contracts.WorkflowTemplate, error) {
	specs := reg.Specs()
	byType := make(map[string]engine.Spec, len(specs))
	for _, sp := range specs {
		byType[sp.Type] = sp
	}

	candidates := []contracts.WorkflowTemplate{
		{
			ID:          "studio.clean_and_inspect",
			Name:        "Clean Watermark & Inspect Contact Sheet",
			Description: "Removes burned-in corner logos from a generated video clip and extracts a labeled JPEG contact sheet with BLACK/FREEZE/CUT signals.",
			Category:    "qc",
			Params: []contracts.WorkflowTemplateParam{
				{Name: "clean.video_path", Description: "Input video file path", Required: true},
				{Name: "clean.mode", Description: "delogo or crop", Default: "delogo"},
				{Name: "inspect.output_dir", Description: "Directory for contact sheet JPEGs"},
			},
			Graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "clean", Type: contracts.NodeTypeVideoWMRemove},
					{ID: "inspect", Type: contracts.NodeTypeVideoContactSheet},
				},
				Edges: []engine.Edge{
					{
						From: engine.Endpoint{Node: "clean", Port: "video"},
						To:   engine.Endpoint{Node: "inspect", Port: "video_path"},
					},
				},
			},
		},
		{
			ID:          "studio.dub_and_master",
			Name:        "TTS Dub Mux & Contact Sheet QC",
			Description: "Synthesizes or muxes dubbing voiceover onto a shot video with overflow alignment and generates a labeled contact sheet for verification.",
			Category:    "post_production",
			Params: []contracts.WorkflowTemplateParam{
				{Name: "dub.video_path", Description: "Input shot video file path", Required: true},
				{Name: "dub.audio_path", Description: "Optional pre-generated dub audio path"},
				{Name: "dub.text", Description: "Narration or dialogue text when audio_path is omitted"},
				{Name: "dub.overflow_mode", Description: "freeze_last_frame | speed_up | trim", Default: "freeze_last_frame"},
			},
			Graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "dub", Type: contracts.NodeTypeTTSDubMux},
					{ID: "inspect", Type: contracts.NodeTypeVideoContactSheet},
				},
				Edges: []engine.Edge{
					{
						From: engine.Endpoint{Node: "dub", Port: "video"},
						To:   engine.Endpoint{Node: "inspect", Port: "video_path"},
					},
				},
			},
		},
		{
			ID:          "studio.grid_to_timeline",
			Name:        "Grid Split & Timeline Render Pipeline",
			Description: "Splits a storyboard grid image into individual panels and assembles + renders the episode EditTimeline into a loudness-normalized MP4.",
			Category:    "assembly",
			Params: []contracts.WorkflowTemplateParam{
				{Name: "split.image_path", Description: "Storyboard grid image path", Required: true},
				{Name: "split.rows", Description: "Grid rows", Default: "2"},
				{Name: "split.cols", Description: "Grid cols", Default: "2"},
				{Name: "assemble.shots_json", Description: "ShotPromptPackage or Shot[] JSON", Required: true},
				{Name: "assemble.overrun_policy", Description: "warn | freeze_last_frame | speed_fit", Default: "warn"},
				{Name: "render.output_dir", Description: "Export output folder"},
			},
			Graph: engine.Graph{
				Nodes: []engine.GraphNode{
					{ID: "split", Type: contracts.NodeTypeGridSplit},
					{ID: "assemble", Type: contracts.NodeTypeTimelineAutoAssem},
					{ID: "render", Type: contracts.NodeTypeRenderTimeline},
				},
				Edges: []engine.Edge{
					{
						From: engine.Endpoint{Node: "assemble", Port: "timeline_json"},
						To:   engine.Endpoint{Node: "render", Port: "timeline_json"},
					},
				},
			},
		},
	}

	// Filter to templates whose node types are all registered (so minimal test registries without
	// optional external-provider nodes still validate), and strictly verify all edges/ports.
	var verified []contracts.WorkflowTemplate
	seenIDs := make(map[string]bool, len(candidates))
	for _, tpl := range candidates {
		if strings.TrimSpace(tpl.ID) == "" || strings.TrimSpace(tpl.Name) == "" {
			return nil, fmt.Errorf("workflow template missing ID or Name")
		}
		if seenIDs[tpl.ID] {
			return nil, fmt.Errorf("duplicate workflow template ID %q", tpl.ID)
		}
		seenIDs[tpl.ID] = true

		allPresent := true
		for _, gn := range tpl.Graph.Nodes {
			if _, ok := byType[gn.Type]; !ok {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}
		if err := validateTemplateGraph(byType, tpl); err != nil {
			return nil, err
		}
		verified = append(verified, tpl)
	}
	return verified, nil
}

func validateTemplateGraph(byType map[string]engine.Spec, tpl contracts.WorkflowTemplate) error {
	if len(tpl.Graph.Nodes) == 0 {
		return fmt.Errorf("workflow template %q has no nodes", tpl.ID)
	}
	nodeTypes := make(map[string]engine.Spec, len(tpl.Graph.Nodes))
	for _, gn := range tpl.Graph.Nodes {
		if strings.TrimSpace(gn.ID) == "" {
			return fmt.Errorf("workflow template %q has node with empty ID", tpl.ID)
		}
		if _, dup := nodeTypes[gn.ID]; dup {
			return fmt.Errorf("workflow template %q has duplicate node ID %q", tpl.ID, gn.ID)
		}
		sp, ok := byType[gn.Type]
		if !ok {
			return fmt.Errorf("workflow template %q references unregistered node %q", tpl.ID, gn.Type)
		}
		nodeTypes[gn.ID] = sp
	}

	adj := make(map[string][]string)
	indeg := make(map[string]int)
	for _, e := range tpl.Graph.Edges {
		fromSp, okFrom := nodeTypes[e.From.Node]
		toSp, okTo := nodeTypes[e.To.Node]
		if !okFrom || !okTo {
			return fmt.Errorf("workflow template %q edge %s->%s references unknown node", tpl.ID, e.From.Node, e.To.Node)
		}
		outPort, okOut := findPortSpec(fromSp.Outputs, e.From.Port)
		if !okOut {
			return fmt.Errorf("workflow template %q missing output port %s.%s", tpl.ID, e.From.Node, e.From.Port)
		}
		inPort, okIn := findPortSpec(toSp.Inputs, e.To.Port)
		if !okIn {
			return fmt.Errorf("workflow template %q missing input port %s.%s", tpl.ID, e.To.Node, e.To.Port)
		}
		if !engine.Compatible(outPort.Type, inPort.Type) {
			return fmt.Errorf("workflow template %q incompatible edge %s.%s (%s) -> %s.%s (%s)",
				tpl.ID, e.From.Node, outPort.Name, outPort.Type, e.To.Node, inPort.Name, inPort.Type)
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
		indeg[e.To.Node]++
	}

	// Cycle check (Kahn's algorithm)
	var ready []string
	for _, gn := range tpl.Graph.Nodes {
		if indeg[gn.ID] == 0 {
			ready = append(ready, gn.ID)
		}
	}
	visited := 0
	for len(ready) > 0 {
		cur := ready[0]
		ready = ready[1:]
		visited++
		for _, nxt := range adj[cur] {
			indeg[nxt]--
			if indeg[nxt] == 0 {
				ready = append(ready, nxt)
			}
		}
	}
	if visited != len(tpl.Graph.Nodes) {
		return fmt.Errorf("workflow template %q contains a cycle", tpl.ID)
	}
	return nil
}

func findPortSpec(ports []engine.Port, name string) (engine.Port, bool) {
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return engine.Port{}, false
}

// Templates returns the verified reusable Studio DAG workflow templates.
func (s *Service) Templates() []contracts.WorkflowTemplate {
	if s == nil || len(s.templates) == 0 {
		return []contracts.WorkflowTemplate{}
	}
	out := make([]contracts.WorkflowTemplate, len(s.templates))
	copy(out, s.templates)
	return out
}
