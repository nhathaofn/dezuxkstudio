package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"app/internal/engine"
)

// maxNodeInputFiles caps how many files one agent-initiated node run may receive after folder expansion.
const maxNodeInputFiles = 10000

// preparedNodeInput is a node input normalized and validated against the node's Spec, exactly as the
// feature's own UI would send it. It is generic: any registered node benefits without node-specific code.
type preparedNodeInput struct {
	Input       map[string]any
	Files       []string // every input file the run will read
	Folders     []string // folders the agent passed (expanded into Files)
	OutputDirs  []string
	Destructive []string // labels of destructive toggles that are enabled (e.g. "Overwrite original file")
	Defaults    []string // params filled from Spec defaults
}

// summary describes the run for approval prompts and tool results.
func (p preparedNodeInput) summary(spec engine.Spec) string {
	var sb strings.Builder
	sb.WriteString(spec.Label)
	if n := len(p.Files); n > 0 {
		fmt.Fprintf(&sb, " on %d file(s)", n)
	}
	if len(p.Folders) > 0 {
		fmt.Fprintf(&sb, " from %s", strings.Join(p.Folders, ", "))
	}
	if len(p.OutputDirs) > 0 {
		fmt.Fprintf(&sb, ", output to %s", strings.Join(p.OutputDirs, ", "))
	}
	if len(p.Destructive) > 0 {
		fmt.Fprintf(&sb, " — %s", strings.Join(p.Destructive, ", "))
	}
	return sb.String()
}

func specByType(specs []engine.Spec, nodeType string) (engine.Spec, bool) {
	for _, sp := range specs {
		if sp.Type == nodeType {
			return sp, true
		}
	}
	return engine.Spec{}, false
}

// prepareNodeInput validates input against spec. fedPorts are input ports supplied by graph edges.
func (s *Service) prepareNodeInput(spec engine.Spec, input map[string]any, fedPorts map[string]bool, recursive bool) (preparedNodeInput, error) {
	out := preparedNodeInput{Input: make(map[string]any, len(input))}

	known := make(map[string]bool, len(spec.Inputs)+len(spec.Params))
	var names []string
	for _, p := range spec.Inputs {
		known[p.Name] = true
		names = append(names, p.Name)
	}
	for _, p := range spec.Params {
		known[p.Name] = true
		names = append(names, p.Name)
	}
	for k := range input {
		if !known[k] {
			return out, fmt.Errorf("node %s has no input or setting named %q; valid names: %s (see workflow_list_specs)", spec.Type, k, strings.Join(names, ", "))
		}
	}

	seenFiles := make(map[string]bool)
	providedPorts := 0
	for _, port := range spec.Inputs {
		raw, ok := input[port.Name]
		if !ok || raw == nil || fedPorts[port.Name] {
			if fedPorts[port.Name] {
				providedPorts++
			}
			continue
		}
		if port.Type == engine.PortText {
			out.Input[port.Name] = raw
			providedPorts++
			continue
		}
		paths, err := mediaPathsFromValue(raw)
		if err != nil {
			return out, fmt.Errorf("input %q: %w", port.Name, err)
		}
		var files []string
		for _, p := range paths {
			expanded, folder, err := s.expandMediaPath(p, port.Type, recursive)
			if err != nil {
				return out, fmt.Errorf("input %q: %w", port.Name, err)
			}
			if folder != "" {
				out.Folders = append(out.Folders, folder)
			}
			for _, f := range expanded {
				if !seenFiles[pathKey(f)] {
					seenFiles[pathKey(f)] = true
					files = append(files, f)
				}
			}
		}
		if len(files) == 0 {
			continue
		}
		if !port.Multiple && len(files) > 1 {
			hint := ""
			for _, other := range spec.Inputs {
				if other.Multiple && engine.Compatible(port.Type, other.Type) {
					hint = fmt.Sprintf("; pass folders or several files to %q instead", other.Name)
				}
			}
			return out, fmt.Errorf("input %q accepts a single file but got %d%s", port.Name, len(files), hint)
		}
		out.Files = append(out.Files, files...)
		if len(out.Files) > maxNodeInputFiles {
			return out, fmt.Errorf("too many input files (%d, limit %d); choose a smaller folder", len(out.Files), maxNodeInputFiles)
		}
		if port.Multiple {
			out.Input[port.Name] = files
		} else {
			out.Input[port.Name] = files[0]
		}
		providedPorts++
	}
	for _, port := range spec.Inputs {
		if !port.Optional && out.Input[port.Name] == nil && !fedPorts[port.Name] {
			return out, fmt.Errorf("input %q (%s) is required", port.Name, port.Label)
		}
	}
	if len(spec.Inputs) > 0 && providedPorts == 0 {
		return out, fmt.Errorf("no input files found for %s; give a file or folder path containing %s", spec.Label, portTypeHint(spec.Inputs[0].Type))
	}

	for _, param := range spec.Params {
		raw, ok := input[param.Name]
		if !ok || raw == nil {
			if param.Default == "" {
				continue
			}
			v, err := parseParamDefault(param)
			if err != nil {
				return out, err
			}
			out.Input[param.Name] = v
			out.Defaults = append(out.Defaults, fmt.Sprintf("%s=%s", param.Name, param.Default))
			raw = v
		} else {
			v, err := s.validateParam(param, raw)
			if err != nil {
				return out, err
			}
			out.Input[param.Name] = v
			raw = v
		}
		if param.Role == engine.ParamRoleOutputDir {
			if dir, _ := raw.(string); dir != "" {
				out.OutputDirs = append(out.OutputDirs, dir)
			}
		}
		if param.Destructive && raw == true {
			out.Destructive = append(out.Destructive, param.Label)
		}
	}
	return out, nil
}

func (s *Service) validateParam(param engine.Param, raw any) (any, error) {
	switch param.Kind {
	case engine.ParamToggle:
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			if b, err := strconv.ParseBool(v); err == nil {
				return b, nil
			}
		}
		return nil, fmt.Errorf("setting %q must be true or false", param.Name)
	case engine.ParamInteger:
		var n float64
		switch v := raw.(type) {
		case float64:
			n = v
		case int:
			n = float64(v)
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("setting %q must be an integer", param.Name)
			}
			n = f
		default:
			return nil, fmt.Errorf("setting %q must be an integer", param.Name)
		}
		if n != math.Trunc(n) {
			return nil, fmt.Errorf("setting %q must be an integer", param.Name)
		}
		if (param.Min != 0 || param.Max != 0) && (int(n) < param.Min || int(n) > param.Max) {
			return nil, fmt.Errorf("setting %q must be between %d and %d", param.Name, param.Min, param.Max)
		}
		return int(n), nil
	case engine.ParamSelect:
		str, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("setting %q must be one of: %s", param.Name, strings.Join(param.Options, ", "))
		}
		if len(param.Options) > 0 {
			for _, o := range param.Options {
				if strings.EqualFold(o, strings.TrimSpace(str)) {
					return o, nil
				}
			}
			return nil, fmt.Errorf("setting %q must be one of: %s (got %q)", param.Name, strings.Join(param.Options, ", "), str)
		}
		return str, nil
	default: // text
		str, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("setting %q must be text", param.Name)
		}
		str = strings.TrimSpace(str)
		if param.Role == engine.ParamRoleOutputDir && str != "" {
			dir, err := s.policy.Resolve(str)
			if err != nil {
				return nil, fmt.Errorf("setting %q: %w", param.Name, err)
			}
			if st, err := os.Stat(dir); err == nil && !st.IsDir() {
				return nil, fmt.Errorf("setting %q must be a folder, %q is a file", param.Name, dir)
			}
			return dir, nil
		}
		return str, nil
	}
}

func parseParamDefault(param engine.Param) (any, error) {
	switch param.Kind {
	case engine.ParamToggle:
		b, err := strconv.ParseBool(param.Default)
		if err != nil {
			return nil, fmt.Errorf("node spec: invalid default for %q", param.Name)
		}
		return b, nil
	case engine.ParamInteger:
		n, err := strconv.Atoi(param.Default)
		if err != nil {
			return nil, fmt.Errorf("node spec: invalid default for %q", param.Name)
		}
		return n, nil
	}
	return param.Default, nil
}

// mediaPathsFromValue accepts a path string, a MediaRef object, or a list of either.
func mediaPathsFromValue(raw any) ([]string, error) {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		return []string{v}, nil
	case map[string]any:
		if u, ok := v["localUrl"].(string); ok && u != "" {
			return []string{u}, nil
		}
		return nil, fmt.Errorf("expected a file path or {\"localUrl\": path}")
	case []any:
		var out []string
		for _, el := range v {
			ps, err := mediaPathsFromValue(el)
			if err != nil {
				return nil, err
			}
			out = append(out, ps...)
		}
		return out, nil
	case []string:
		return v, nil
	}
	return nil, fmt.Errorf("expected a file/folder path or a list of paths")
}

func portAccepts(t engine.PortType, c FileClass) bool {
	switch t {
	case engine.PortImage:
		return c == FileClassImage
	case engine.PortVideo:
		return c == FileClassVideo
	case engine.PortMedia:
		return c == FileClassImage || c == FileClassVideo
	}
	return false
}

func portTypeHint(t engine.PortType) string {
	switch t {
	case engine.PortImage:
		return "images (.png, .jpg, .jpeg, .webp, …)"
	case engine.PortVideo:
		return "videos (.mp4, .mov, .webm, …)"
	case engine.PortMedia:
		return "images or videos"
	}
	return "files"
}

// expandMediaPath validates one path; folders expand to their matching media files (sorted).
func (s *Service) expandMediaPath(raw string, portType engine.PortType, recursive bool) ([]string, string, error) {
	p := raw
	if !filepath.IsAbs(strings.TrimSpace(p)) && s.mediaDir != "" {
		// Same convention as the nodes: relative media paths live in DezuxkStudio's media dir.
		p = filepath.Join(s.mediaDir, strings.TrimPrefix(strings.TrimPrefix(filepath.ToSlash(p), "media/"), "/"))
	}
	resolved, err := s.policy.Resolve(p)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("%q does not exist", raw)
	}
	if !st.IsDir() {
		if c := ClassifyFile(resolved); !portAccepts(portType, c) {
			return nil, "", fmt.Errorf("%q is not one of the accepted %s", filepath.Base(resolved), portTypeHint(portType))
		}
		return []string{resolved}, "", nil
	}

	var files []string
	walkErr := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, like the OS file picker would
		}
		if d.IsDir() {
			if path != resolved && (!recursive || !s.policy.VisibleInListing(path, true)) {
				return filepath.SkipDir
			}
			return nil
		}
		if portAccepts(portType, ClassifyFile(path)) && s.policy.VisibleInListing(path, false) {
			files = append(files, path)
			if len(files) > maxNodeInputFiles {
				return fs.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, "", fmt.Errorf("folder %q contains no %s", resolved, portTypeHint(portType))
	}
	return files, resolved, nil
}

// prepareGraph validates every node of a graph; inputs fed by edges are not required in params.
func (s *Service) prepareGraph(specs []engine.Spec, g engine.Graph, recursive bool) (engine.Graph, []preparedNodeInput, error) {
	fed := make(map[string]map[string]bool)
	for _, e := range g.Edges {
		if fed[e.To.Node] == nil {
			fed[e.To.Node] = make(map[string]bool)
		}
		fed[e.To.Node][e.To.Port] = true
	}
	out := engine.Graph{Edges: g.Edges, Nodes: make([]engine.GraphNode, 0, len(g.Nodes))}
	var prepared []preparedNodeInput
	for _, n := range g.Nodes {
		spec, ok := specByType(specs, n.Type)
		if !ok {
			return out, nil, fmt.Errorf("node %q has unknown type %q; use workflow_list_specs", n.ID, n.Type)
		}
		input := map[string]any{}
		if len(n.Params) > 0 {
			if err := json.Unmarshal(n.Params, &input); err != nil {
				return out, nil, fmt.Errorf("node %q params must be a JSON object: %w", n.ID, err)
			}
		}
		prep, err := s.prepareNodeInput(spec, input, fed[n.ID], recursive)
		if err != nil {
			return out, nil, fmt.Errorf("node %q: %w", n.ID, err)
		}
		raw, err := json.Marshal(prep.Input)
		if err != nil {
			return out, nil, err
		}
		out.Nodes = append(out.Nodes, engine.GraphNode{ID: n.ID, Type: n.Type, Params: raw})
		prepared = append(prepared, prep)
	}
	return out, prepared, nil
}
