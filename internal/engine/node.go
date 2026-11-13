// Package engine provides DezuxkStudio's modular node execution runtime.
// It supports standalone step execution and directed acyclic graph (DAG) pipelines.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
)

// PortType is the kind of value travelling between nodes.
type PortType string

const (
	PortText  PortType = "text"
	PortImage PortType = "image"
	PortVideo PortType = "video"
	PortAudio PortType = "audio"
	PortMedia PortType = "media" // Accepts image or video
)

// AllPortTypes is bound to the frontend as a TypeScript enum.
var AllPortTypes = []struct {
	Value  PortType
	TSName string
}{{PortText, "Text"}, {PortImage, "Image"}, {PortVideo, "Video"}, {PortAudio, "Audio"}, {PortMedia, "Media"}}

// Port is one input or output of a node.
type Port struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     PortType `json:"type"`
	Multiple bool     `json:"multiple"`
	Optional bool     `json:"optional"`
}

// ParamKind is how a node setting is chosen.
type ParamKind string

const (
	ParamSelect  ParamKind = "select"
	ParamInteger ParamKind = "integer"
	ParamText    ParamKind = "text"
	ParamToggle  ParamKind = "toggle"
)

// AllParamKinds is bound to the frontend as a TypeScript enum.
var AllParamKinds = []struct {
	Value  ParamKind
	TSName string
}{{ParamSelect, "Select"}, {ParamInteger, "Integer"}, {ParamText, "Text"}, {ParamToggle, "Toggle"}}

// ParamRole tells callers (UI, agent) what a text param refers to so they can validate it generically.
type ParamRole string

const (
	ParamRoleNone      ParamRole = ""
	ParamRoleOutputDir ParamRole = "output_dir" // directory the node writes results into
)

// Param is a node setting.
type Param struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Kind        ParamKind `json:"kind"`
	OptionsFrom string    `json:"optionsFrom,omitempty"`
	Min         int       `json:"min,omitempty"`
	Max         int       `json:"max,omitempty"`
	// Options lists the allowed values of a select param.
	Options []string `json:"options,omitempty"`
	// Default is the value the UI uses when the user does not change the setting (string form).
	Default string `json:"default,omitempty"`
	// Role marks params that carry a filesystem path.
	Role ParamRole `json:"role,omitempty"`
	// Destructive marks a toggle that, when true, modifies or replaces the user's original files.
	Destructive bool `json:"destructive,omitempty"`
	// Secret marks a text param that carries a credential (API key...). Its value is never shown in
	// summaries or approval cards, and the agent removes it from the stored chat history after a run.
	Secret bool `json:"secret,omitempty"`
}

// Spec describes a node type: identity, ports and settings.
//
// The Spec is also the contract DezuxkStudio's agent reads to use the feature: fill Description, Options,
// Default, Role and Destructive so the agent runs the node exactly like its UI. See
// docs/agent-feature-integration.md.
type Spec struct {
	Type        string  `json:"type"`
	Provider    string  `json:"provider"`
	Label       string  `json:"label"`
	Description string  `json:"description"`
	Inputs      []Port  `json:"inputs"`
	Outputs     []Port  `json:"outputs"`
	Params      []Param `json:"params"`
}

// Node runs one unit of work.
type Node interface {
	Spec() Spec
	Run(ctx context.Context, input json.RawMessage) (any, error)
}

// Typed adapts a function with a typed request and result into a Node.
func Typed[In, Out any](spec Spec, run func(context.Context, In) (Out, error)) Node {
	return typedNode[In, Out]{spec: spec, run: run}
}

type typedNode[In, Out any] struct {
	spec Spec
	run  func(context.Context, In) (Out, error)
}

func (n typedNode[In, Out]) Spec() Spec { return n.spec }

func (n typedNode[In, Out]) Run(ctx context.Context, input json.RawMessage) (any, error) {
	var in In
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, Invalid(fmt.Sprintf("Invalid input data for node %s: %v", n.spec.Type, err))
		}
	}
	return n.run(ctx, in)
}
