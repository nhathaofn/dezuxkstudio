package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ValidateSpec checks that a node's Spec is complete enough for every caller to use it without
// node-specific code: the UI renders it, and the agent (workflow_list_specs / workflow_run_node)
// reads it as its only contract. The workflow service runs it for every production node at
// startup, so an incomplete spec fails fast instead of producing a feature the agent misuses.
func ValidateSpec(s Spec) error {
	var errs []error
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("node %q: "+format, append([]any{s.Type}, a...)...))
	}

	if s.Type == "" {
		return errors.New("node spec without a type")
	}
	if strings.TrimSpace(s.Label) == "" {
		add("label is required (shown in the UI and in approval cards)")
	}
	if strings.TrimSpace(s.Description) == "" {
		add("description is required (the agent reads it to decide when to use the node)")
	}

	names := map[string]bool{}
	for _, p := range append(append([]Port{}, s.Inputs...), s.Outputs...) {
		if p.Name == "" {
			add("port without a name")
		}
		if !slices.ContainsFunc(AllPortTypes, func(t struct {
			Value  PortType
			TSName string
		}) bool {
			return t.Value == p.Type
		}) {
			add("port %q has unknown type %q", p.Name, p.Type)
		}
	}
	for _, p := range s.Inputs {
		names[p.Name] = true
	}

	for _, p := range s.Params {
		if p.Name == "" {
			add("param without a name")
			continue
		}
		if names[p.Name] {
			add("param %q duplicates an input or param name", p.Name)
		}
		names[p.Name] = true
		if strings.TrimSpace(p.Label) == "" {
			add("param %q: label is required", p.Name)
		}
		if p.Destructive && p.Kind != ParamToggle {
			add("param %q: only toggles can be destructive (the approval gate checks toggles)", p.Name)
		}
		if p.Role == ParamRoleOutputDir && p.Kind != ParamText {
			add("param %q: output_dir role requires kind text", p.Name)
		}
		switch p.Kind {
		case ParamSelect:
			if len(p.Options) == 0 && p.OptionsFrom == "" {
				add("param %q: select needs Options or OptionsFrom", p.Name)
			}
			if p.Default != "" && len(p.Options) > 0 && !slices.ContainsFunc(p.Options, func(o string) bool { return strings.EqualFold(o, p.Default) }) {
				add("param %q: default %q is not one of the options", p.Name, p.Default)
			}
		case ParamInteger:
			if p.Max != 0 && p.Min > p.Max {
				add("param %q: min %d > max %d", p.Name, p.Min, p.Max)
			}
			if p.Default != "" {
				v, err := strconv.Atoi(p.Default)
				switch {
				case err != nil:
					add("param %q: default %q is not an integer", p.Name, p.Default)
				case v < p.Min || (p.Max != 0 && v > p.Max):
					add("param %q: default %d outside [%d, %d]", p.Name, v, p.Min, p.Max)
				}
			}
		case ParamToggle:
			if p.Default != "" && p.Default != "true" && p.Default != "false" {
				add("param %q: toggle default must be \"true\" or \"false\"", p.Name)
			}
			if p.Destructive && p.Default == "true" {
				add("param %q: a destructive toggle must default to false", p.Name)
			}
		case ParamText:
		default:
			add("param %q has unknown kind %q", p.Name, p.Kind)
		}
	}
	return errors.Join(errs...)
}
